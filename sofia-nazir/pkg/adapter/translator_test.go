package adapter

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	opiv1alpha1 "opi-nvidia-adapter/api/v1alpha1"
)

func TestNvidiaAdapterImplementsVendorAdapter(t *testing.T) {
	var adapterInstance VendorAdapter = &NvidiaAdapter{}
	if adapterInstance == nil {
		t.Fatal("expected adapter to implement the vendor interface")
	}
}

func TestTranslateDPUClusterToDPUSet(t *testing.T) {
	cluster := &opiv1alpha1.DPUCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: opiv1alpha1.DPUClusterSpec{
			Vendor:    "nvidia",
			BFB:       "http://example.com/bfb-v3.bfb",
			DpuFlavor: "high-perf",
			NodeSelector: map[string]string{
				"dpu-node": "true",
			},
		},
	}

	dpuset := TranslateDPUClusterToDPUSet(cluster)
	if dpuset == nil {
		t.Fatalf("expected dpuset to be non-nil")
	}

	if dpuset.Name != "test-cluster-dpuset" {
		t.Errorf("expected name to be test-cluster-dpuset, got %s", dpuset.Name)
	}

	if dpuset.Namespace != "default" {
		t.Errorf("expected namespace default, got %s", dpuset.Namespace)
	}

	if dpuset.Spec.DPUTemplate.Spec.BFB == nil || dpuset.Spec.DPUTemplate.Spec.BFB.Name != "http://example.com/bfb-v3.bfb" {
		t.Errorf("expected BFB reference to match, got %#v", dpuset.Spec.DPUTemplate.Spec.BFB)
	}

	if dpuset.Spec.DPUTemplate.Spec.DPUFlavor != "high-perf" {
		t.Errorf("expected flavor high-perf, got %s", dpuset.Spec.DPUTemplate.Spec.DPUFlavor)
	}

	if dpuset.Spec.DPUNodeSelector == nil || dpuset.Spec.DPUNodeSelector.MatchLabels["dpu-node"] != "true" {
		t.Errorf("expected node selector to map correctly")
	}
}

func TestTranslateDPUClusterToDPUSet_DefaultFlavor(t *testing.T) {
	cluster := &opiv1alpha1.DPUCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-cluster",
		},
		Spec: opiv1alpha1.DPUClusterSpec{
			Vendor: "nvidia",
		},
	}

	dpuset := TranslateDPUClusterToDPUSet(cluster)
	if dpuset.Spec.DPUTemplate.Spec.DPUFlavor != "default" {
		t.Errorf("expected default flavor, got %s", dpuset.Spec.DPUTemplate.Spec.DPUFlavor)
	}
}

func TestTranslateDPUClusterToDPUService(t *testing.T) {
	cluster := &opiv1alpha1.DPUCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
		},
		Spec: opiv1alpha1.DPUClusterSpec{
			Vendor:             "nvidia",
			NetworkOffloadMode: "ovn-kubernetes",
			VpcName:            "prod-vpc",
		},
	}

	service := TranslateDPUClusterToDPUService(cluster, "test-cluster-dpuset")
	if service == nil {
		t.Fatalf("expected service to be non-nil")
	}

	if service.Name != "test-cluster-dpuservice" {
		t.Errorf("expected name to be test-cluster-dpuservice, got %s", service.Name)
	}

	if service.Spec.ServiceID == nil || *service.Spec.ServiceID != "test-cluster-service" {
		t.Errorf("expected service ID to be populated, got %#v", service.Spec.ServiceID)
	}

	if service.Spec.DeployInCluster == nil || *service.Spec.DeployInCluster {
		t.Errorf("expected deployInCluster to default to false")
	}
}
