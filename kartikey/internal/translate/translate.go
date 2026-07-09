// Package translate turns OPI-side desired state into the DPF object(s) from
// the architecture's CRD mapping table (arch §3). It is pure: no client, no
// I/O — just OPI types + fleet params in, unstructured DPF objects out. That
// keeps the mapping unit-testable and keeps the Reconciler thin.
package translate

import (
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	opiv1 "github.com/opiproject/opi-nvidia-dpf-adapter/api/opi/v1"
	"github.com/opiproject/opi-nvidia-dpf-adapter/internal/dpf"
)

// DefaultDPUFlavor is the admin-authored flavor the adapter references when the
// OPI side expresses no DPF-specific provisioning detail (arch §3, DPUFlavor
// row: OPI has no BFB/firmware field, so we default it here).
const DefaultDPUFlavor = "bf3-default"

// Labels stamped on every adapter-owned DPF object.
const (
	// ManagedByLabel/Value mark objects this adapter owns, so status watches
	// can map a DPF object back to the OPI intent that produced it (arch §5).
	ManagedByLabel = "app.kubernetes.io/managed-by"
	ManagedByValue = "opi-nvidia-dpf-adapter"
	// OwnerRefLabel back-references the OPI object (namespace_name) this DPF
	// object was derived from — used to requeue the right OPI object.
	OwnerRefLabel = "dpu.nvidia.com/owner"
	// FleetLabel records which fleet owns the object (arch §9, Q4).
	FleetLabel = "dpu.nvidia.com/fleet"
)

// TargetFleet is the subset of per-fleet info the translator needs. It is
// defined here (not imported from package controller) so translate has no
// dependency on controller — controller imports translate, and a reverse edge
// would be an import cycle. controller.Fleet converts to this at the call site.
type TargetFleet struct {
	// Name scopes object names so two fleets with an identically-named SFC
	// never collide on one DPF object (arch §9, Q4).
	Name string
	// Namespace is where this fleet's DPF objects are created.
	Namespace string
	// NodeSelector is the fleet's DPUSet selector — DPF fans ONE deployment
	// out across all matching nodes (arch §9, Q1: reuse DPF's fan-out).
	NodeSelector map[string]string
	// DPUFlavor overrides DefaultDPUFlavor when set.
	DPUFlavor string
}

func (f TargetFleet) flavor() string {
	if f.DPUFlavor != "" {
		return f.DPUFlavor
	}
	return DefaultDPUFlavor
}

// fleetName defaults an empty fleet name so single-fleet deployments still get
// stable, non-colliding object names.
func (f TargetFleet) fleetName() string {
	if f.Name != "" {
		return f.Name
	}
	return "default"
}

// Translator converts OPI intent into DPF unstructured objects. An interface so
// alternate mappings (e.g. one-cluster vs two-cluster topology) are swappable.
type Translator interface {
	// DeploymentForFleet builds the ONE fleet-level DPUDeployment that
	// provisions every DPU the fleet's node selector matches. This is
	// deliberately per-FLEET, not per-node: DPF's DPUSet already fans a single
	// deployment out to N nodes, so minting one per node would reimplement
	// DPF's grouping (arch §9, Q1).
	DeploymentForFleet(fleet TargetFleet) *unstructured.Unstructured

	// ChainForSFC builds the DPUServiceChain + DPUServiceInterface(s) +
	// DPUService(s) for an OPI ServiceFunctionChain, name-scoped to the fleet.
	ChainForSFC(fleet TargetFleet, sfc *opiv1.ServiceFunctionChain) []*unstructured.Unstructured
}

// DPFTranslator is the default two-cluster/static-DPUCluster implementation.
// It is stateless; all per-fleet inputs arrive as TargetFleet.
type DPFTranslator struct{}

// NewDPFTranslator returns the default translator.
func NewDPFTranslator() *DPFTranslator { return &DPFTranslator{} }

var _ Translator = (*DPFTranslator)(nil)

// ownerValue encodes an OPI object identity for the back-reference label.
func ownerValue(o metav1.Object) string {
	return fmt.Sprintf("%s_%s", o.GetNamespace(), o.GetName())
}

// base builds an unstructured shell with GVK, name, namespace and the standard
// labels every adapter-owned DPF object carries.
func base(fleet TargetFleet, apiVersion, kind, name, owner string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion(apiVersion)
	u.SetKind(kind)
	u.SetName(name)
	u.SetNamespace(fleet.Namespace)
	u.SetLabels(map[string]string{
		ManagedByLabel: ManagedByValue,
		OwnerRefLabel:  owner,
		FleetLabel:     fleet.fleetName(),
	})
	return u
}

// DeploymentForFleet implements Translator.
func (t *DPFTranslator) DeploymentForFleet(fleet TargetFleet) *unstructured.Unstructured {
	name := "dpudeploy-" + fleet.fleetName()
	// Owner is the fleet itself, not any single DPU (arch §9, Q1).
	u := base(fleet, dpf.ProvisioningGroup+"/"+dpf.Version, dpf.DPUDeploymentGVK.Kind, name, "fleet_"+fleet.fleetName())

	sel := map[string]interface{}{}
	for k, v := range fleet.NodeSelector {
		sel[k] = v
	}
	_ = unstructured.SetNestedField(u.Object, fleet.flavor(), "spec", "dpuFlavor")
	_ = unstructured.SetNestedMap(u.Object, sel, "spec", "nodeSelector")
	return u
}

// ChainForSFC implements Translator.
func (t *DPFTranslator) ChainForSFC(fleet TargetFleet, sfc *opiv1.ServiceFunctionChain) []*unstructured.Unstructured {
	owner := ownerValue(sfc)
	objs := make([]*unstructured.Unstructured, 0, len(sfc.Spec.NetworkFunctions)*2+1)

	// Prefix every object name with the fleet so two fleets never collide.
	prefix := fleet.fleetName() + "-" + sfc.Name

	hopNames := make([]interface{}, 0, len(sfc.Spec.NetworkFunctions))
	ifaceRefs := make([]interface{}, 0, len(sfc.Spec.NetworkFunctions))

	for i, nf := range sfc.Spec.NetworkFunctions {
		// One DPUService per NF workload (SFC NF -> DPUService).
		svcName := fmt.Sprintf("%s-%s", prefix, nf.Name)
		svc := base(fleet, dpf.SvcGroup+"/"+dpf.Version, dpf.DPUServiceGVK.Kind, svcName, owner)
		_ = unstructured.SetNestedField(svc.Object, nf.Image, "spec", "image")
		objs = append(objs, svc)

		// One DPUServiceInterface per hop (SFC port -> DPUServiceInterface).
		ifName := fmt.Sprintf("%s-if-%d", prefix, i)
		iface := base(fleet, dpf.SvcGroup+"/"+dpf.Version, dpf.DPUServiceInterfaceGVK.Kind, ifName, owner)
		_ = unstructured.SetNestedField(iface.Object, svcName, "spec", "service")
		objs = append(objs, iface)

		hopNames = append(hopNames, nf.Name)
		ifaceRefs = append(ifaceRefs, ifName)
	}

	// One DPUServiceChain tying the hops/interfaces together (SFC -> chain).
	chain := base(fleet, dpf.SvcGroup+"/"+dpf.Version, dpf.DPUServiceChainGVK.Kind, "chain-"+prefix, owner)
	_ = unstructured.SetNestedSlice(chain.Object, hopNames, "spec", "nodes")
	_ = unstructured.SetNestedSlice(chain.Object, ifaceRefs, "spec", "interfaceRefs")
	objs = append(objs, chain)

	return objs
}
