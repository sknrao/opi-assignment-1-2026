package adapter

import (
	"context"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	opiv1 "github.com/opiproject/opi-nvidia-dpf-adapter/api/opi/v1"
)

// fleet.go — companion to feature_skeleton.go (same package `adapter`). It holds
// the two abstractions the self-review (arch §9) added:
//
//  1. Vendor scoping — so this NVIDIA adapter NEVER acts on an Intel/Marvell
//     object. ServiceFunctionChain and DataProcessingUnit are vendor-NEUTRAL
//     OPI CRDs; without a filter the adapter would translate a non-NVIDIA SFC
//     into DPF objects and corrupt another vendor's workload. That is the
//     adapter's real blast radius, and this is the isolation boundary.
//
//  2. Fleet resolution — so more than one DPUCluster / independent BlueField
//     fleet can have separate lifecycles, credentials and namespaces. A Fleet
//     bundles everything that is per-fleet; the ClusterResolver maps an OPI
//     object to its Fleet (or reports it is not ours).

// Vendor scoping keys. The AUTHORITATIVE signal for a DataProcessingUnit is its
// spec.dpuProductName (the daemon fills it at autodetection; `oc get dpu` prints
// it as "DPU Product") — verified against openshift/dpu-operator api/v1. So for
// a DPU we scope on the real product field and need no extra label. For the
// user-authored ServiceFunctionChain (which has no product field) we fall back
// to a vendor label in OPI's real `dpu.config.openshift.io` domain — the same
// domain as the shipping `dpu.config.openshift.io/dpuside` selector — which the
// NVIDIA detector/admission would stamp (arch §8, assumption 10).
const (
	VendorLabel  = "dpu.config.openshift.io/vendor"
	VendorNVIDIA = "nvidia"
	// FleetLabel selects which fleet an object belongs to in a multi-fleet
	// deployment. Absent => the single default fleet.
	FleetLabel = "dpu.nvidia.com/fleet"
)

// isNVIDIAManaged reports whether an object is owned by NVIDIA hardware and thus
// in scope for this adapter. Used both as an informer predicate (cheap pre-
// filter) and as a defense-in-depth guard inside Reconcile.
func isNVIDIAManaged(obj client.Object) bool {
	// A DataProcessingUnit carries its vendor in spec.dpuProductName — the real,
	// daemon-populated field — so prefer it over any label.
	if dpu, ok := obj.(*opiv1.DataProcessingUnit); ok && isNVIDIAProduct(dpu.Spec.DpuProductName) {
		return true
	}
	return obj.GetLabels()[VendorLabel] == VendorNVIDIA
}

// isNVIDIAProduct matches the "DPU Product" string OPI records for BlueField.
func isNVIDIAProduct(product string) bool {
	p := strings.ToLower(product)
	return strings.Contains(p, "bluefield") || strings.Contains(p, "nvidia")
}

// Fleet is everything that is scoped to one independent BlueField fleet /
// DPUCluster: where its DPF objects live, how its DPUSet selects nodes, and how
// to probe its DPU-cluster reachability + credential. Two fleets are fully
// isolated — separate namespaces, separate probes, separate object names.
type Fleet struct {
	// Name is the fleet identifier; used to namespace object names so two
	// fleets with an identically-named SFC never collide (arch §9, Q4).
	Name string
	// Namespace is where this fleet's DPF objects are created.
	Namespace string
	// NodeSelector is the fleet's DPUSet selector. The adapter creates ONE
	// fleet-level DPUDeployment and lets DPF's DPUSet fan out to nodes, rather
	// than minting one DPUDeployment per node (arch §9, Q1: reuse DPF's fleet
	// grouping instead of reimplementing it per-node).
	NodeSelector map[string]string
	// DPUFlavor is the admin-authored flavor for this fleet.
	DPUFlavor string
	// Probe reports this fleet's DPU-cluster reachability (its own credential).
	Probe DPUClusterProbe
}

// ClusterResolver maps an OPI object to the Fleet that should service it. ok is
// false when the object is not NVIDIA-managed (or belongs to no known fleet),
// in which case the adapter MUST NOT touch it.
type ClusterResolver interface {
	Resolve(ctx context.Context, obj client.Object) (fleet Fleet, ok bool, err error)
}

// SingleFleetResolver is the v1 wiring: exactly one fleet. It still enforces
// vendor scoping, so even with one fleet an Intel object is rejected. A real
// multi-fleet resolver keys on FleetLabel and looks up per-fleet credentials;
// the interface is identical, so upgrading is drop-in (arch §9, Q4).
type SingleFleetResolver struct {
	Fleet Fleet
}

var _ ClusterResolver = (*SingleFleetResolver)(nil)

// Resolve returns the single fleet iff the object is NVIDIA-managed.
//
// TODO(dpuf): ServiceFunctionChain is user-authored and may not carry the
// vendor label directly; the production resolver walks SFC -> target
// DataProcessingUnit -> vendor label. The skeleton uses the direct label check
// as a stand-in but the guard (reject-if-not-ours) is the actual fix.
func (r *SingleFleetResolver) Resolve(_ context.Context, obj client.Object) (Fleet, bool, error) {
	if !isNVIDIAManaged(obj) {
		return Fleet{}, false, nil
	}
	return r.Fleet, true, nil
}
