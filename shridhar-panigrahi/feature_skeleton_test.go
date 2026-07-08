package nvidiavsp

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The design document claims the interesting logic is testable without
// BlueField hardware because it lives in pure functions. These tests cash
// that check for the three claims that matter most: identity derivation,
// status projection, and the version matrix.

func TestDpuIdentifierDerivation(t *testing.T) {
	d := NewNvidiaBlueFieldDetector()
	cases := []struct {
		name    string
		pci     PCIDevice
		want    DpuIdentifier
		wantErr bool
	}{
		{
			name: "serial normalises to lower case",
			pci:  PCIDevice{Address: "0000:03:00.0", Serial: "MT2333XZ0ABC"},
			want: "nvidia-bf3-mt2333xz0abc",
		},
		{
			name:    "missing serial is an error, not a guess",
			pci:     PCIDevice{Address: "0000:03:00.0"},
			wantErr: true,
		},
	}
	for _, c := range cases {
		got, err := d.GetDpuIdentifier(nil, &c.pci)
		if c.wantErr != (err != nil) {
			t.Fatalf("%s: err = %v, wantErr = %v", c.name, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Fatalf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestMultiPortDeduplication(t *testing.T) {
	d := NewNvidiaBlueFieldDetector()
	// Two PCI functions, same board serial: one DPU, not two.
	port0 := PCIDevice{Address: "0000:03:00.0", VendorID: pciVendorNvidia, DeviceID: "a2dc", Serial: "MT42"}
	port1 := PCIDevice{Address: "0000:03:00.1", VendorID: pciVendorNvidia, DeviceID: "a2dc", Serial: "MT42"}

	ok, err := d.IsDPU(nil, port0, nil)
	if err != nil || !ok {
		t.Fatalf("first port should be detected: ok=%v err=%v", ok, err)
	}
	id, _ := d.GetDpuIdentifier(nil, &port0)
	ok, err = d.IsDPU(nil, port1, []DpuIdentifier{id})
	if err != nil || ok {
		t.Fatalf("second port of the same board must deduplicate: ok=%v err=%v", ok, err)
	}
}

func TestProjectionIsPureAndLevelTriggered(t *testing.T) {
	now := metav1.Now()
	ready := &Object{Phase: "Ready"}
	failed := &Object{Phase: "Error", Conditions: []metav1.Condition{{
		Type: "Provisioned", Status: metav1.ConditionFalse,
		Message: "DMS: rshim write failed at 62%",
	}}}

	cases := []struct {
		name       string
		dpu        *Object
		reachable  bool
		wantStatus metav1.ConditionStatus
		wantReason string
	}{
		{"no DPF object yet", nil, true, metav1.ConditionFalse, "AwaitingProvisioning"},
		{"cluster partition wins over stale phase", ready, false, metav1.ConditionFalse, "DpuClusterUnreachable"},
		{"ready projects true", ready, true, metav1.ConditionTrue, "Provisioned"},
		{"failure carries the DMS message verbatim", failed, true, metav1.ConditionFalse, "ProvisioningFailed"},
	}
	for _, c := range cases {
		got := ProjectDpuConditions(c.dpu, c.reachable, now)
		if len(got) == 0 || got[0].Status != c.wantStatus || got[0].Reason != c.wantReason {
			t.Fatalf("%s: got %+v", c.name, got)
		}
	}

	// Purity: same observed state must project identically no matter how
	// often or in what order it is evaluated - nothing may latch.
	a := ProjectDpuConditions(failed, true, now)
	ProjectDpuConditions(ready, true, now) // interleaved observation
	b := ProjectDpuConditions(failed, true, now)
	if a[0].Reason != b[0].Reason || a[0].Message != b[0].Message {
		t.Fatalf("projection latched state: %+v vs %+v", a[0], b[0])
	}
	if a[0].Message != "DMS: rshim write failed at 62%" {
		t.Fatalf("DMS message not passed through verbatim: %q", a[0].Message)
	}
}

func TestVersionMatrixIsExactNotFuzzy(t *testing.T) {
	m := VersionMatrix{
		BridgeVersion: "v0.3",
		Tested: []VersionPair{
			{DpuOperator: "v1.0", Dpf: "25.4"},
			{DpuOperator: "v1.1", Dpf: "25.7"},
		},
	}
	if !m.Supported("v1.0", "25.4") {
		t.Fatal("tested pair must be supported")
	}
	// Cross-combinations of individually-known versions are NOT implied:
	// only pairs that actually went through the conformance suite count.
	if m.Supported("v1.0", "25.7") {
		t.Fatal("untested cross-combination must be rejected")
	}
	if m.Supported("v1.2", "25.7") {
		t.Fatal("unknown version must be rejected")
	}
}
