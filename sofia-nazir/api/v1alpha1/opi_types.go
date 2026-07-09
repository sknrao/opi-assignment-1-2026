package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DPUClusterSpec defines the desired state of DPUCluster
type DPUClusterSpec struct {
	// Vendor specifies the hardware vendor. e.g., "nvidia", "intel", "marvell"
	Vendor string `json:"vendor"`

	// NetworkOffloadMode specifies the configuration mode for networking (e.g. "ovn-kubernetes", "ovs-dpdk")
	NetworkOffloadMode string `json:"networkOffloadMode,omitempty"`

	// BFB specifies the BlueField Bootstream firmware image to install (NVIDIA specific)
	BFB string `json:"bfb,omitempty"`

	// DpuFlavor specifies the configuration flavor for the DPUs (NVIDIA specific)
	DpuFlavor string `json:"dpuFlavor,omitempty"`

	// NodeSelector defines labels to select the worker nodes containing the DPUs
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// VpcName defines the name of the VPC for mapping
	VpcName string `json:"vpcName,omitempty"`
}

// DPUClusterStatus defines the observed state of DPUCluster
type DPUClusterStatus struct {
	// Ready indicates whether all DPUs in the cluster are provisioned and ready
	Ready bool `json:"ready"`

	// Phase indicates the current lifecycle phase (e.g. Pending, Provisioning, Ready, Failed)
	Phase string `json:"phase,omitempty"`

	// Conditions represent the latest available observations of an object's state
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DPUCluster is the Schema for the dpuclusters API
type DPUCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DPUClusterSpec   `json:"spec,omitempty"`
	Status DPUClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DPUClusterList contains a list of DPUCluster
type DPUClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DPUCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DPUCluster{}, &DPUClusterList{})
}

// DeepCopyInto copies the receiver, writing into out. in must be non-nil.
func (in *DPUCluster) DeepCopyInto(out *DPUCluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

// DeepCopy creates a new DPUCluster.
func (in *DPUCluster) DeepCopy() *DPUCluster {
	if in == nil {
		return nil
	}
	out := new(DPUCluster)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject creates a new runtime.Object.
func (in *DPUCluster) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// DeepCopyInto copies the receiver, writing into out. in must be non-nil.
func (in *DPUClusterSpec) DeepCopyInto(out *DPUClusterSpec) {
	*out = *in
	if in.NodeSelector != nil {
		in, out := &in.NodeSelector, &out.NodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

// DeepCopyInto copies the receiver, writing into out. in must be non-nil.
func (in *DPUClusterStatus) DeepCopyInto(out *DPUClusterStatus) {
	*out = *in
	if in.Conditions != nil {
		in, out := &in.Conditions, &out.Conditions
		*out = make([]metav1.Condition, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopyInto copies the receiver, writing into out. in must be non-nil.
func (in *DPUClusterList) DeepCopyInto(out *DPUClusterList) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ListMeta.DeepCopyInto(&out.ListMeta)
	if in.Items != nil {
		in, out := &in.Items, &out.Items
		*out = make([]DPUCluster, len(*in))
		for i := range *in {
			(*in)[i].DeepCopyInto(&(*out)[i])
		}
	}
}

// DeepCopy creates a new DPUClusterList.
func (in *DPUClusterList) DeepCopy() *DPUClusterList {
	if in == nil {
		return nil
	}
	out := new(DPUClusterList)
	in.DeepCopyInto(out)
	return out
}

// DeepCopyObject creates a new runtime.Object.
func (in *DPUClusterList) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}
