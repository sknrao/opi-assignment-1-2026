package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// LOCAL MIRROR of github.com/openshift/dpu-operator's DataProcessingUnit
// (config.openshift.io/v1). Verified field-for-field against upstream api/v1 on
// 2026-07-03 via `gh api`: the CRD is cluster-scoped, shortName "dpu" (so
// `oc get dpu` / `kubectl get dpu` list this type — see architecture_design.md
// §11), spec = {dpuProductName, isDpuSide, nodeName}, status = {conditions}.
// It is created BY the dpu-daemon after PCI autodetection — never by a human and
// never by this adapter. The adapter only reads it (vendor + node) and writes
// back status/annotations.
//
// NOTE: the real status carries NO datapath-endpoint or phase field — the VSP's
// Init() returns the endpoint over gRPC. So the adapter publishes the
// DPF-provisioned endpoint via the EndpointAnnotation below (not a status
// field), and the thin NVIDIA VSP returns it from Init(). See §3 / §5.

const (
	// IntentAnnotation is where the NVIDIA VSP records the desired action it
	// could not satisfy synchronously (provision / chain), for the cluster
	// controller to pick up. Reusing an annotation adds zero OPI API surface.
	IntentAnnotation = "dpu.nvidia.com/adapter-intent"

	// EndpointAnnotation is where the adapter publishes the DPF-provisioned
	// datapath ip:port. The real DataProcessingUnit has no status field for it,
	// so an annotation is the honest channel; the VSP returns it from Init().
	EndpointAnnotation = "dpu.nvidia.com/datapath-endpoint"
)

// DataProcessingUnitSpec mirrors the upstream spec exactly.
type DataProcessingUnitSpec struct {
	// DpuProductName is the vendor+model of the DPU, e.g. "NVIDIA BlueField-3".
	// The daemon populates it at autodetection; `oc get dpu` prints it as the
	// "DPU Product" column. This is the AUTHORITATIVE vendor signal the adapter
	// scopes on (arch §9-Q3) — no separate vendor label is strictly required.
	DpuProductName string `json:"dpuProductName,omitempty"`
	// IsDpuSide is true for the DPU-side inventory entry and false for the
	// host-side one; each physical BlueField yields one of each (arch §11).
	IsDpuSide bool `json:"isDpuSide"`
	// NodeName is the node this DPU is attached to / runs as.
	NodeName string `json:"nodeName"`
}

// DataProcessingUnitStatus mirrors the upstream status: conditions only. The
// printed STATUS column is `.status.conditions[?(@.type=='Ready')].status`, so
// the "Ready" condition is what `oc get dpu` shows. Ownership: the OPI daemon
// owns "Ready"; this adapter contributes the granular "DPFReady" (arch §5).
type DataProcessingUnitStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=dpu
// +kubebuilder:printcolumn:name="DPU Product",type="string",JSONPath=".spec.dpuProductName"
// +kubebuilder:printcolumn:name="DPU Side",type="boolean",JSONPath=".spec.isDpuSide"
// +kubebuilder:printcolumn:name="Node Name",type="string",JSONPath=".spec.nodeName"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type=='Ready')].status"

// DataProcessingUnit is the per-node DPU inventory object (mirrored).
type DataProcessingUnit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DataProcessingUnitSpec   `json:"spec,omitempty"`
	Status DataProcessingUnitStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DataProcessingUnitList is the list type.
type DataProcessingUnitList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DataProcessingUnit `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DataProcessingUnit{}, &DataProcessingUnitList{})
}

// --- hand-written deepcopy (see note in servicefunctionchain_types.go) ---

// DeepCopyInto copies the receiver into out.
func (in *DataProcessingUnit) DeepCopyInto(out *DataProcessingUnit) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec // all value fields
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		for i := range in.Status.Conditions {
			in.Status.Conditions[i].DeepCopyInto(&out.Status.Conditions[i])
		}
	}
}

// DeepCopy returns a deep copy of the receiver.
func (in *DataProcessingUnit) DeepCopy() *DataProcessingUnit {
	if in == nil {
		return nil
	}
	out := new(DataProcessingUnit)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject satisfies runtime.Object.
func (in *DataProcessingUnit) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the list.
func (in *DataProcessingUnitList) DeepCopyInto(out *DataProcessingUnitList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]DataProcessingUnit, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy of the list.
func (in *DataProcessingUnitList) DeepCopy() *DataProcessingUnitList {
	if in == nil {
		return nil
	}
	out := new(DataProcessingUnitList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject satisfies runtime.Object.
func (in *DataProcessingUnitList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
