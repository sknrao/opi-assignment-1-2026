package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// LOCAL MIRROR of github.com/openshift/dpu-operator's ServiceFunctionChain
// (config.openshift.io/v1, shortName "sfc"). Verified against upstream api/v1 on
// 2026-07-03 via `gh api`: spec = {nodeSelector, networkFunctions[]{name,image}}.

// NetworkFunction is one hop in an ordered service-function chain. Matches the
// upstream NF entry exactly (name + image reference for the NF workload).
type NetworkFunction struct {
	// Name is the stable identifier of this NF within the chain.
	Name string `json:"name"`
	// Image is the NF workload image. On the DPF side this becomes the
	// source for a DPUService (Helm-via-ArgoCD) — see translate.go.
	Image string `json:"image"`
}

// ServiceFunctionChainSpec is the user-authored intent. Mirrors upstream:
// an optional node selector plus the ordered NF list.
type ServiceFunctionChainSpec struct {
	// NodeSelector restricts which nodes may host the NF pods; empty = all
	// nodes. In this adapter it also informs which fleet an SFC belongs to
	// (arch §9-Q4).
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// NetworkFunctions is the ordered chain the user wants realized on the DPU.
	NetworkFunctions []NetworkFunction `json:"networkFunctions"`
}

// ServiceFunctionChainStatus is where the adapter mirrors DPF's chain status
// back up so `kubectl get servicefunctionchain` stays truthful (see arch §5).
type ServiceFunctionChainStatus struct {
	// Conditions carries the aggregated, least-ready DPF condition mapped onto
	// OPI semantics (Ready / Degraded with a DPF-derived reason).
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// ObservedGeneration is the .metadata.generation the status reflects.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sfc

// ServiceFunctionChain is the OPI-level SFC intent object (mirrored).
type ServiceFunctionChain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServiceFunctionChainSpec   `json:"spec,omitempty"`
	Status ServiceFunctionChainStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServiceFunctionChainList is the list type.
type ServiceFunctionChainList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ServiceFunctionChain `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ServiceFunctionChain{}, &ServiceFunctionChainList{})
}

// ---------------------------------------------------------------------------
// runtime.Object / DeepCopy implementations.
//
// In a real Kubebuilder project these live in zz_generated.deepcopy.go and are
// produced by controller-gen. They are hand-written here so the module compiles
// without running code generation; the shapes are exactly what controller-gen
// would emit.
// ---------------------------------------------------------------------------

// DeepCopyInto copies the receiver into out.
func (in *ServiceFunctionChain) DeepCopyInto(out *ServiceFunctionChain) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	if in.Spec.NodeSelector != nil {
		out.Spec.NodeSelector = make(map[string]string, len(in.Spec.NodeSelector))
		for k, v := range in.Spec.NodeSelector {
			out.Spec.NodeSelector[k] = v
		}
	}
	if in.Spec.NetworkFunctions != nil {
		out.Spec.NetworkFunctions = make([]NetworkFunction, len(in.Spec.NetworkFunctions))
		copy(out.Spec.NetworkFunctions, in.Spec.NetworkFunctions)
	}
	if in.Status.Conditions != nil {
		out.Status.Conditions = make([]metav1.Condition, len(in.Status.Conditions))
		for i := range in.Status.Conditions {
			in.Status.Conditions[i].DeepCopyInto(&out.Status.Conditions[i])
		}
	}
}

// DeepCopy returns a deep copy of the receiver.
func (in *ServiceFunctionChain) DeepCopy() *ServiceFunctionChain {
	if in == nil {
		return nil
	}
	out := new(ServiceFunctionChain)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject satisfies runtime.Object.
func (in *ServiceFunctionChain) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the list.
func (in *ServiceFunctionChainList) DeepCopyInto(out *ServiceFunctionChainList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		out.Items = make([]ServiceFunctionChain, len(in.Items))
		for i := range in.Items {
			in.Items[i].DeepCopyInto(&out.Items[i])
		}
	}
}

// DeepCopy returns a deep copy of the list.
func (in *ServiceFunctionChainList) DeepCopy() *ServiceFunctionChainList {
	if in == nil {
		return nil
	}
	out := new(ServiceFunctionChainList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject satisfies runtime.Object.
func (in *ServiceFunctionChainList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
