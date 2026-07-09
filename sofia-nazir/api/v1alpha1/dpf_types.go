package v1alpha1

import (
	provisioningv1 "github.com/nvidia/doca-platform/api/provisioning/v1alpha1"
	dpuservicev1 "github.com/nvidia/doca-platform/api/dpuservice/v1alpha1"
)

// DPUSet aliases the upstream NVIDIA DPF DPUSet type so the adapter can work with the real vendor CRD.
type DPUSet = provisioningv1.DPUSet

// DPUSetList aliases the upstream NVIDIA DPF DPUSetList type.
type DPUSetList = provisioningv1.DPUSetList

// DPUSetSpec aliases the upstream NVIDIA DPF DPUSetSpec type.
type DPUSetSpec = provisioningv1.DPUSetSpec

// DPUSetStatus aliases the upstream NVIDIA DPF DPUSetStatus type.
type DPUSetStatus = provisioningv1.DPUSetStatus

// DPUService aliases the upstream NVIDIA DPF DPUService type.
type DPUService = dpuservicev1.DPUService

// DPUServiceList aliases the upstream NVIDIA DPF DPUServiceList type.
type DPUServiceList = dpuservicev1.DPUServiceList

// DPUServiceSpec aliases the upstream NVIDIA DPF DPUServiceSpec type.
type DPUServiceSpec = dpuservicev1.DPUServiceSpec

// DPUServiceStatus aliases the upstream NVIDIA DPF DPUServiceStatus type.
type DPUServiceStatus = dpuservicev1.DPUServiceStatus

func init() {
	SchemeBuilder.Register(&provisioningv1.DPUSet{}, &provisioningv1.DPUSetList{}, &dpuservicev1.DPUService{}, &dpuservicev1.DPUServiceList{})
}
