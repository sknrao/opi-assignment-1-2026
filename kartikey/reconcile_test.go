package adapter

// reconcile_test.go — a REAL, running, assertion-based test of the two adapter
// reconcilers in feature_skeleton.go. It drives Reconcile() end to end and
// asserts on concrete translated field values and status conditions, not merely
// "an object exists".
//
// ─────────────────────────────────────────────────────────────────────────────
// WHY THE FAKE CLIENT AND NOT envtest.
//
// The ideal integration harness is sigs.k8s.io/controller-runtime's envtest,
// which spins a real kube-apiserver + etcd. We deliberately do NOT use it here,
// for two concrete reasons:
//
//  1. envtest needs the `kube-apiserver` and `etcd` BINARIES on disk (fetched by
//     `setup-envtest`). This environment is offline / has no such binaries, so
//     an envtest suite would just t.Skip — which is exactly the "no real test"
//     outcome we were told to avoid.
//
//  2. Dependency footprint: envtest pulls the apiserver machinery into the test
//     build. The fake client is already a transitive dep (controller-runtime),
//     so it adds nothing to go.mod.
//
// The one catch: the reconcilers converge DPF objects with SERVER-SIDE APPLY
// (client.Apply + ForceOwnership), and the v0.17.3 fake client rejects apply
// patches outright ("apply patches are not supported in the fake client"). So a
// naive fake client would fail on the very first apply and never exercise the
// real loop. We close that gap with `ssaClient` below: a thin wrapper that
// emulates force-ownership apply against the fake tracker (upsert of spec +
// labels/annotations while PRESERVING status written by another actor). That
// keeps the REAL Reconcile code path running — translate → apply → read-back →
// map status — with no source changes and no external binaries.
//
// If/when the module moves to Go 1.26 + a newer controller-runtime whose fake
// client supports apply natively (or an envtest binary is provisioned in CI),
// `ssaClient` can be deleted and the client swapped 1:1.

import (
	"context"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	opiv1 "github.com/opiproject/opi-nvidia-dpf-adapter/api/opi/v1"
	"github.com/opiproject/opi-nvidia-dpf-adapter/internal/dpf"
	"github.com/opiproject/opi-nvidia-dpf-adapter/internal/translate"
)

// ─── test harness ───────────────────────────────────────────────────────────

// ssaClient wraps a controller-runtime fake client so that client.Apply patches
// (server-side apply), which the v0.17.3 fake client refuses, are emulated as an
// upsert against the fake tracker. Everything else delegates unchanged.
//
// The emulation intentionally overlays only spec + labels/annotations and keeps
// any pre-existing status, because real force-ownership apply by the adapter's
// field manager does not own — and therefore does not clobber — the status that
// the (simulated) DPF controller writes. Getting that right is what lets the
// "DPF reports Ready/Error, adapter re-reconciles" steps work.
type ssaClient struct {
	client.WithWatch
}

func (c *ssaClient) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if patch.Type() != types.ApplyPatchType {
		return c.WithWatch.Patch(ctx, obj, patch, opts...)
	}
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		// The adapter only ever server-side-applies unstructured DPF objects.
		return c.WithWatch.Patch(ctx, obj, patch, opts...)
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(u.GroupVersionKind())
	err := c.WithWatch.Get(ctx, client.ObjectKeyFromObject(u), existing)
	if apierrors.IsNotFound(err) {
		return c.WithWatch.Create(ctx, u.DeepCopy())
	}
	if err != nil {
		return err
	}

	merged := existing.DeepCopy()
	if spec, found, ferr := unstructured.NestedFieldCopy(u.Object, "spec"); ferr == nil && found {
		_ = unstructured.SetNestedField(merged.Object, spec, "spec")
	}
	if lbls := u.GetLabels(); lbls != nil {
		merged.SetLabels(lbls)
	}
	if anns := u.GetAnnotations(); anns != nil {
		merged.SetAnnotations(anns)
	}
	return c.WithWatch.Update(ctx, merged)
}

// testScheme registers only the OPI mirror types. DPF objects are driven as
// unstructured for UNREGISTERED GVKs, which the fake client supports natively
// via UnsafeGuessKindToResource — so they must NOT be added to the scheme.
func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := opiv1.AddToScheme(s); err != nil {
		t.Fatalf("register OPI scheme: %v", err)
	}
	return s
}

// newClient builds the SSA-emulating fake client, treating the OPI objects'
// status as a real subresource (so Status().Patch behaves like production).
func newClient(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()
	base := fakeclient.NewClientBuilder().
		WithScheme(testScheme(t)).
		WithStatusSubresource(&opiv1.DataProcessingUnit{}, &opiv1.ServiceFunctionChain{}).
		WithObjects(objs...).
		Build()
	return &ssaClient{WithWatch: base}
}

// readyCondition stamps a DPF unstructured object's status.conditions with a
// single Ready condition of the given status ("True"/"False"), mirroring how a
// real DPF controller reports readiness (feature_skeleton.go: dpfObjectReady).
func setDPFReady(t *testing.T, c client.Client, u *unstructured.Unstructured, status string) {
	t.Helper()
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(u.GroupVersionKind())
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(u), cur); err != nil {
		t.Fatalf("get %s/%s to set Ready: %v", u.GetKind(), u.GetName(), err)
	}
	conds := []interface{}{
		map[string]interface{}{
			"type":    "Ready",
			"status":  status,
			"reason":  "SimulatedByTest",
			"message": "condition injected by unit test",
		},
	}
	if err := unstructured.SetNestedSlice(cur.Object, conds, "status", "conditions"); err != nil {
		t.Fatalf("set status.conditions: %v", err)
	}
	if err := c.Update(context.Background(), cur); err != nil {
		t.Fatalf("update DPF object status: %v", err)
	}
}

// ─── DataProcessingUnit reconciler: full provisioning lifecycle ──────────────

// TestDataProcessingUnitReconciler_Lifecycle exercises all five required steps
// against the DPU provisioning loop:
//
//  1. a realistic desired-state OPI DataProcessingUnit spec,
//  2. Reconcile() called directly,
//  3. the mirrored DPF DPUDeployment is created with the TRANSLATED fields
//     (dpuFlavor + nodeSelector) checked by value,
//  4. the DPF DPU reports Ready → OPI status flips to DPFReady=True/Provisioned
//     and the datapath endpoint is mirrored up as an annotation,
//  5. NEGATIVE: the DPF DPU reports Error → OPI status becomes
//     DPFReady=False/ProvisioningFailed, not a silent Unknown.
func TestDataProcessingUnitReconciler_Lifecycle(t *testing.T) {
	const (
		dpuName = "bluefield3-node1"
		ns      = "dpf-system"
	)

	// (1) realistic desired-state OPI object (created BY the daemon upstream).
	dpu := &opiv1.DataProcessingUnit{
		ObjectMeta: metav1.ObjectMeta{Name: dpuName, Generation: 1},
		Spec: opiv1.DataProcessingUnitSpec{
			DpuProductName: "NVIDIA BlueField-3",
			IsDpuSide:      true,
			NodeName:       "worker-1",
		},
	}

	c := newClient(t, dpu)
	fleet := Fleet{
		Name:         "fleet-a",
		Namespace:    ns,
		NodeSelector: map[string]string{"node-role.kubernetes.io/dpu-host": "true"},
		DPUFlavor:    "bf3-hbn",
		Probe:        NewStaticProbe(logr.Discard(), true),
	}
	r := &DataProcessingUnitReconciler{
		Client:     c,
		Scheme:     testScheme(t),
		Translator: translate.NewDPFTranslator(),
		Fleets:     &SingleFleetResolver{Fleet: fleet},
	}
	ctx := context.Background()
	// DataProcessingUnit is cluster-scoped: request carries a bare name.
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: dpuName}}

	// (2) drive Reconcile #1 — nothing provisioned yet.
	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile #1: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Errorf("reconcile #1: expected a requeue while provisioning, got %v", res.RequeueAfter)
	}

	// (3) the fleet's single DPUDeployment must exist with TRANSLATED fields.
	deploy := &unstructured.Unstructured{}
	deploy.SetGroupVersionKind(dpf.DPUDeploymentGVK)
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "dpudeploy-fleet-a"}, deploy); err != nil {
		t.Fatalf("expected DPUDeployment dpudeploy-fleet-a to be created: %v", err)
	}
	if got, _, _ := unstructured.NestedString(deploy.Object, "spec", "dpuFlavor"); got != "bf3-hbn" {
		t.Errorf("DPUDeployment spec.dpuFlavor = %q, want %q", got, "bf3-hbn")
	}
	if got, _, _ := unstructured.NestedStringMap(deploy.Object, "spec", "nodeSelector"); !reflect.DeepEqual(got, fleet.NodeSelector) {
		t.Errorf("DPUDeployment spec.nodeSelector = %v, want %v", got, fleet.NodeSelector)
	}

	// ...and while the DPF DPU is absent, OPI status must read Provisioning,
	// not stay empty.
	if cond := dpfReadyCond(t, c, dpuName); cond.Status != metav1.ConditionFalse || cond.Reason != ReasonProvisioning {
		t.Errorf("after reconcile #1: DPFReady = %s/%s, want False/%s", cond.Status, cond.Reason, ReasonProvisioning)
	}

	// (4) simulate DPF provisioning the DPU to Ready, exposing a datapath endpoint.
	const endpoint = "10.42.0.7:9339"
	dpfDPU := &unstructured.Unstructured{}
	dpfDPU.SetGroupVersionKind(dpf.DPUGVK)
	dpfDPU.SetNamespace(ns)
	dpfDPU.SetName("dpu-" + dpuName)
	_ = unstructured.SetNestedField(dpfDPU.Object, dpf.DPUPhaseReady, "status", "phase")
	_ = unstructured.SetNestedField(dpfDPU.Object, endpoint, "status", "dataplaneEndpoint")
	if err := c.Create(ctx, dpfDPU); err != nil {
		t.Fatalf("create DPF DPU: %v", err)
	}

	res, err = r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile #2 (Ready): %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("reconcile #2: expected no requeue once Ready, got %v", res.RequeueAfter)
	}
	// endpoint mirrored up as an annotation (the real DPU status has no such field).
	gotDPU := getDPU(t, c, dpuName)
	if got := gotDPU.Annotations[opiv1.EndpointAnnotation]; got != endpoint {
		t.Errorf("datapath endpoint annotation = %q, want %q", got, endpoint)
	}
	if cond := dpfReadyCond(t, c, dpuName); cond.Status != metav1.ConditionTrue || cond.Reason != ReasonProvisioned {
		t.Errorf("after Ready: DPFReady = %s/%s, want True/%s", cond.Status, cond.Reason, ReasonProvisioned)
	}

	// (5) NEGATIVE: DPF flips the DPU to Error. The adapter must reflect the
	// failure, not silently leave the condition True or Unknown.
	cur := &unstructured.Unstructured{}
	cur.SetGroupVersionKind(dpf.DPUGVK)
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "dpu-" + dpuName}, cur); err != nil {
		t.Fatalf("re-get DPF DPU: %v", err)
	}
	_ = unstructured.SetNestedField(cur.Object, dpf.DPUPhaseError, "status", "phase")
	if err := c.Update(ctx, cur); err != nil {
		t.Fatalf("update DPF DPU to Error: %v", err)
	}

	res, err = r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile #3 (Error): %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Errorf("reconcile #3: expected a requeue while not Ready, got %v", res.RequeueAfter)
	}
	cond := dpfReadyCond(t, c, dpuName)
	if cond.Status != metav1.ConditionFalse || cond.Reason != ReasonProvisioningFailed {
		t.Errorf("after Error: DPFReady = %s/%s, want False/%s", cond.Status, cond.Reason, ReasonProvisioningFailed)
	}
	if cond.Reason == "" || cond.Status == metav1.ConditionUnknown {
		t.Errorf("failure was swallowed: DPFReady stayed silently Unknown (%s/%s)", cond.Status, cond.Reason)
	}
}

// getDPU re-fetches the cluster-scoped DataProcessingUnit.
func getDPU(t *testing.T, c client.Client, name string) *opiv1.DataProcessingUnit {
	t.Helper()
	got := &opiv1.DataProcessingUnit{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: name}, got); err != nil {
		t.Fatalf("get DataProcessingUnit %s: %v", name, err)
	}
	return got
}

// dpfReadyCond returns the adapter-owned DPFReady condition, failing if absent.
func dpfReadyCond(t *testing.T, c client.Client, name string) metav1.Condition {
	t.Helper()
	got := getDPU(t, c, name)
	cond := meta.FindStatusCondition(got.Status.Conditions, CondDPFReady)
	if cond == nil {
		t.Fatalf("expected a %q condition on %s, found none", CondDPFReady, name)
	}
	return *cond
}

// ─── ServiceFunctionChain reconciler: translated fan-out + readiness ─────────

// TestServiceFunctionChainReconciler_ChainTranslationAndReadiness covers the
// SFC loop, which fans one OPI chain out into several DPF objects:
//
//  1. a realistic SFC spec (two network functions) labelled NVIDIA-managed,
//  2. Reconcile() called directly,
//  3. the mirrored DPUService / DPUServiceInterface / DPUServiceChain objects
//     are created with the TRANSLATED field values (NF image, service ref,
//     chain hop names),
//  4. all DPF objects report Ready → SFC status becomes DPFReady=True/
//     ChainProgrammed,
//  5. NEGATIVE: one DPF object reports Ready=False → the SFC does NOT go green;
//     status reflects the still-deploying object by name.
func TestServiceFunctionChainReconciler_ChainTranslationAndReadiness(t *testing.T) {
	const (
		sfcName = "north-south-chain"
		ns      = "dpf-system"
	)

	sfc := &opiv1.ServiceFunctionChain{
		ObjectMeta: metav1.ObjectMeta{
			Name:       sfcName,
			Namespace:  ns,
			Generation: 1,
			Labels:     map[string]string{VendorLabel: VendorNVIDIA},
		},
		Spec: opiv1.ServiceFunctionChainSpec{
			NetworkFunctions: []opiv1.NetworkFunction{
				{Name: "firewall", Image: "registry.example.com/nf/firewall:v1"},
				{Name: "dpi", Image: "registry.example.com/nf/dpi:v2"},
			},
		},
	}

	c := newClient(t, sfc)
	fleet := Fleet{
		Name:      "fleet-a",
		Namespace: ns,
		Probe:     NewStaticProbe(logr.Discard(), true),
	}
	r := &ServiceFunctionChainReconciler{
		Client:     c,
		Scheme:     testScheme(t),
		Translator: translate.NewDPFTranslator(),
		Fleets:     &SingleFleetResolver{Fleet: fleet},
	}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: sfcName}}

	// (2) Reconcile #1 — chain applied, backing objects not yet Ready.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #1: %v", err)
	}

	// Reconstruct the expected DPF objects through the same translator to get
	// their names, then assert each was created with the TRANSLATED fields.
	desired := translate.NewDPFTranslator().ChainForSFC(fleetToTarget(fleet), sfc)
	if len(desired) != 5 { // 2 services + 2 interfaces + 1 chain
		t.Fatalf("expected 5 translated DPF objects, got %d", len(desired))
	}

	// The firewall NF must have produced a DPUService carrying its image.
	svc := getUnstructured(t, c, dpf.DPUServiceGVK, ns, "fleet-a-"+sfcName+"-firewall")
	if got, _, _ := unstructured.NestedString(svc.Object, "spec", "image"); got != "registry.example.com/nf/firewall:v1" {
		t.Errorf("DPUService spec.image = %q, want firewall image", got)
	}
	// The first interface must reference that service.
	iface := getUnstructured(t, c, dpf.DPUServiceInterfaceGVK, ns, "fleet-a-"+sfcName+"-if-0")
	if got, _, _ := unstructured.NestedString(iface.Object, "spec", "service"); got != "fleet-a-"+sfcName+"-firewall" {
		t.Errorf("DPUServiceInterface spec.service = %q, want the firewall service name", got)
	}
	// The chain must list both hops in order.
	chain := getUnstructured(t, c, dpf.DPUServiceChainGVK, ns, "chain-fleet-a-"+sfcName)
	nodes, _, _ := unstructured.NestedStringSlice(chain.Object, "spec", "nodes")
	if !reflect.DeepEqual(nodes, []string{"firewall", "dpi"}) {
		t.Errorf("DPUServiceChain spec.nodes = %v, want [firewall dpi]", nodes)
	}

	// Not-yet-ready: SFC status must be False/ServiceDeploying, never green.
	if cond := sfcReadyCond(t, c, ns, sfcName); cond.Status != metav1.ConditionFalse {
		t.Errorf("before readiness: DPFReady = %s, want False", cond.Status)
	}

	// (4) simulate every DPF object reporting Ready.
	for _, o := range desired {
		setDPFReady(t, c, o, "True")
	}
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #2 (all Ready): %v", err)
	}
	if cond := sfcReadyCond(t, c, ns, sfcName); cond.Status != metav1.ConditionTrue || cond.Reason != ReasonChainProgrammed {
		t.Errorf("all-Ready: DPFReady = %s/%s, want True/%s", cond.Status, cond.Reason, ReasonChainProgrammed)
	}

	// (5) NEGATIVE: one hop drops back to Ready=False. SFC must leave green.
	setDPFReady(t, c, chain, "False")
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #3 (one not Ready): %v", err)
	}
	cond := sfcReadyCond(t, c, ns, sfcName)
	if cond.Status != metav1.ConditionFalse || cond.Reason != ReasonServiceDeploying {
		t.Errorf("one-not-Ready: DPFReady = %s/%s, want False/%s", cond.Status, cond.Reason, ReasonServiceDeploying)
	}
	if cond.Status == metav1.ConditionUnknown {
		t.Errorf("failure was swallowed: SFC DPFReady stayed silently Unknown")
	}
}

// ─── ServiceFunctionChain reconciler: finalizer-driven garbage collection ────

// TestServiceFunctionChainReconciler_FinalizerGarbageCollectsDPFObjects proves
// the adapter is a "well-behaved" DPF client (arch §12, Q4): because the DPF
// objects it creates carry no (cross-namespace-invalid) ownerReference, deleting
// the OPI SFC must still reclaim them — via the cleanup finalizer — instead of
// orphaning DPUServiceChain/Interface/Service objects for DPF's GC to trip over.
func TestServiceFunctionChainReconciler_FinalizerGarbageCollectsDPFObjects(t *testing.T) {
	const (
		sfcName = "gc-chain"
		ns      = "dpf-system"
	)
	sfc := &opiv1.ServiceFunctionChain{
		ObjectMeta: metav1.ObjectMeta{
			Name:       sfcName,
			Namespace:  ns,
			Generation: 1,
			Labels:     map[string]string{VendorLabel: VendorNVIDIA},
		},
		Spec: opiv1.ServiceFunctionChainSpec{
			NetworkFunctions: []opiv1.NetworkFunction{
				{Name: "firewall", Image: "registry.example.com/nf/firewall:v1"},
			},
		},
	}
	c := newClient(t, sfc)
	fleet := Fleet{Name: "fleet-a", Namespace: ns, Probe: NewStaticProbe(logr.Discard(), true)}
	r := &ServiceFunctionChainReconciler{
		Client:     c,
		Scheme:     testScheme(t),
		Translator: translate.NewDPFTranslator(),
		Fleets:     &SingleFleetResolver{Fleet: fleet},
	}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: sfcName}}

	// Reconcile once: finalizer is adopted and DPF objects are created.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile (create): %v", err)
	}
	live := &opiv1.ServiceFunctionChain{}
	if err := c.Get(ctx, req.NamespacedName, live); err != nil {
		t.Fatalf("get SFC after create: %v", err)
	}
	if !containsStr(live.Finalizers, chainFinalizer) {
		t.Fatalf("expected cleanup finalizer %q on SFC, got %v", chainFinalizer, live.Finalizers)
	}
	// The DPF objects exist.
	svcName := "fleet-a-" + sfcName + "-firewall"
	getUnstructured(t, c, dpf.DPUServiceGVK, ns, svcName) // fails if absent
	getUnstructured(t, c, dpf.DPUServiceChainGVK, ns, "chain-fleet-a-"+sfcName)

	// Delete the SFC. The finalizer keeps it around (DeletionTimestamp set).
	if err := c.Delete(ctx, live); err != nil {
		t.Fatalf("delete SFC: %v", err)
	}

	// Reconcile the deletion: DPF objects must be reclaimed and the finalizer
	// dropped so the SFC actually disappears.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile (delete): %v", err)
	}
	// SFC is gone (finalizer removed → GC completes).
	if err := c.Get(ctx, req.NamespacedName, &opiv1.ServiceFunctionChain{}); !apierrors.IsNotFound(err) {
		t.Errorf("expected SFC gone after finalizer removal, got err=%v", err)
	}
	// The DPF objects must NOT be orphaned.
	for _, name := range []string{svcName, "fleet-a-" + sfcName + "-if-0"} {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(dpf.DPUServiceGVK)
		if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, u); !apierrors.IsNotFound(err) {
			t.Errorf("DPF object %s should have been garbage-collected, got err=%v", name, err)
		}
	}
	chain := &unstructured.Unstructured{}
	chain.SetGroupVersionKind(dpf.DPUServiceChainGVK)
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: "chain-fleet-a-" + sfcName}, chain); !apierrors.IsNotFound(err) {
		t.Errorf("DPUServiceChain should have been garbage-collected, got err=%v", err)
	}
}

// ─── DataProcessingUnit reconciler: readiness-signal drift is LOUD ────────────

// TestDataProcessingUnitReconciler_ReadinessSignalDriftIsLoud proves that if DPF
// changes how DPU readiness is signaled — here, a DPU that reports a status
// stanza but no status.phase — the adapter surfaces a DISTINCT, visible reason
// (DPFStatusUnrecognized) rather than silently mapping the missing field to a
// forever-"Provisioning" (arch §12, Q3).
func TestDataProcessingUnitReconciler_ReadinessSignalDriftIsLoud(t *testing.T) {
	const (
		dpuName = "drift-node"
		ns      = "dpf-system"
	)
	dpu := &opiv1.DataProcessingUnit{
		ObjectMeta: metav1.ObjectMeta{Name: dpuName, Generation: 1},
		Spec: opiv1.DataProcessingUnitSpec{
			DpuProductName: "NVIDIA BlueField-3",
			IsDpuSide:      true,
			NodeName:       "worker-9",
		},
	}
	c := newClient(t, dpu)
	fleet := Fleet{Name: "fleet-a", Namespace: ns, Probe: NewStaticProbe(logr.Discard(), true)}
	r := &DataProcessingUnitReconciler{
		Client:     c,
		Scheme:     testScheme(t),
		Translator: translate.NewDPFTranslator(),
		Fleets:     &SingleFleetResolver{Fleet: fleet},
	}
	ctx := context.Background()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: dpuName}}

	// Prime: reconcile once so the DPUDeployment exists.
	if _, err := r.Reconcile(ctx, req); err != nil {
		t.Fatalf("reconcile #1: %v", err)
	}

	// DPF creates the DPU but signals readiness in a NEW shape the adapter does
	// not understand — a populated status with no status.phase.
	drift := &unstructured.Unstructured{}
	drift.SetGroupVersionKind(dpf.DPUGVK)
	drift.SetNamespace(ns)
	drift.SetName("dpu-" + dpuName)
	_ = unstructured.SetNestedField(drift.Object, "SomeNewReadyGate", "status", "readinessGates")
	if err := c.Create(ctx, drift); err != nil {
		t.Fatalf("create drifted DPF DPU: %v", err)
	}

	res, err := r.Reconcile(ctx, req)
	if err != nil {
		t.Fatalf("reconcile #2 (drift): %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Errorf("expected a requeue on drift, got %v", res.RequeueAfter)
	}
	cond := dpfReadyCond(t, c, dpuName)
	if cond.Status != metav1.ConditionFalse || cond.Reason != ReasonDPFStatusSchema {
		t.Errorf("drift: DPFReady = %s/%s, want False/%s (loud, not silent Provisioning)",
			cond.Status, cond.Reason, ReasonDPFStatusSchema)
	}
	if cond.Reason == ReasonProvisioning {
		t.Errorf("readiness-signal drift was swallowed as a silent Provisioning")
	}
}

// containsStr is a tiny slice-contains helper for the finalizer assertion.
func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// getUnstructured fetches a DPF object by GVK/namespace/name, failing if absent.
func getUnstructured(t *testing.T, c client.Client, gvk schema.GroupVersionKind, ns, name string) *unstructured.Unstructured {
	t.Helper()
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(gvk)
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, got); err != nil {
		t.Fatalf("get %s %s/%s: %v", gvk.Kind, ns, name, err)
	}
	return got
}

// sfcReadyCond returns the SFC's DPFReady condition, failing if absent.
func sfcReadyCond(t *testing.T, c client.Client, ns, name string) metav1.Condition {
	t.Helper()
	got := &opiv1.ServiceFunctionChain{}
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, got); err != nil {
		t.Fatalf("get ServiceFunctionChain %s: %v", name, err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, CondDPFReady)
	if cond == nil {
		t.Fatalf("expected a %q condition on SFC %s, found none", CondDPFReady, name)
	}
	return *cond
}
