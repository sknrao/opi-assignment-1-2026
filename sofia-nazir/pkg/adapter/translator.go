package adapter

import (
	"fmt"

	provisioningv1 "github.com/nvidia/doca-platform/api/provisioning/v1alpha1"
	dpuservicev1 "github.com/nvidia/doca-platform/api/dpuservice/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	opiv1alpha1 "opi-nvidia-adapter/api/v1alpha1"
)

// VendorAdapter translates an OPI DPUCluster into vendor-specific CRs.
type VendorAdapter interface {
	TranslateDPUSet(cluster *opiv1alpha1.DPUCluster) *opiv1alpha1.DPUSet
	TranslateDPUService(cluster *opiv1alpha1.DPUCluster, dpuSetName string) *opiv1alpha1.DPUService
}

// NvidiaAdapter converts OPI clusters into NVIDIA DPF CRs.
type NvidiaAdapter struct{}

// DefaultVendorAdapter is used when no custom adapter is injected.
var DefaultVendorAdapter VendorAdapter = &NvidiaAdapter{}

// TranslateDPUClusterToDPUSet maps an OPI DPUCluster to a DPF DPUSet.
func TranslateDPUClusterToDPUSet(cluster *opiv1alpha1.DPUCluster) *opiv1alpha1.DPUSet {
	return DefaultVendorAdapter.TranslateDPUSet(cluster)
}

// TranslateDPUClusterToDPUService maps an OPI DPUCluster to a DPF DPUService.
func TranslateDPUClusterToDPUService(cluster *opiv1alpha1.DPUCluster, dpuSetName string) *opiv1alpha1.DPUService {
	return DefaultVendorAdapter.TranslateDPUService(cluster, dpuSetName)
}

// TranslateDPUSet creates an NVIDIA DPUSet from an OPI DPUCluster.
func (a *NvidiaAdapter) TranslateDPUSet(cluster *opiv1alpha1.DPUCluster) *opiv1alpha1.DPUSet {
	if cluster == nil {
		return nil
	}

	selector := &metav1.LabelSelector{}
	if len(cluster.Spec.NodeSelector) > 0 {
		selector.MatchLabels = make(map[string]string, len(cluster.Spec.NodeSelector))
		for key, val := range cluster.Spec.NodeSelector {
			selector.MatchLabels[key] = val
		}
	}

	dpuFlavor := cluster.Spec.DpuFlavor
	if dpuFlavor == "" {
		dpuFlavor = "default"
	}

	dpuSetSpec := provisioningv1.DPUSetSpec{
		Strategy: provisioningv1.DPUSetStrategy{Type: provisioningv1.OnDeleteStrategyType},
		DPUNodeSelector: selector,
		DPUTemplate: provisioningv1.DPUTemplate{
			Spec: provisioningv1.DPUTemplateSpec{
				NodeEffect: provisioningv1.NodeEffect{Action: provisioningv1.Action{NoEffect: boolPtr(true)}},
				DPUFlavor: dpuFlavor,
			},
		},
	}

	if cluster.Spec.BFB != "" {
		dpuSetSpec.DPUTemplate.Spec.BFB = &provisioningv1.BFBReference{Name: cluster.Spec.BFB}
	} else {
		dpuSetSpec.DPUTemplate.Spec.BlueFieldSoftware = &provisioningv1.BlueFieldSoftwareReference{Name: fmt.Sprintf("%s-bfs", cluster.Name)}
	}

	return &opiv1alpha1.DPUSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-dpuset", cluster.Name),
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				"opi.io/managed-by": "opi-nvidia-adapter",
				"opi.io/cluster":    cluster.Name,
			},
		},
		Spec: dpuSetSpec,
	}
}

// TranslateDPUService creates an NVIDIA DPUService from an OPI DPUCluster.
func (a *NvidiaAdapter) TranslateDPUService(cluster *opiv1alpha1.DPUCluster, dpuSetName string) *opiv1alpha1.DPUService {
	if cluster == nil || cluster.Spec.NetworkOffloadMode == "" {
		return nil
	}

	deployInCluster := false
	serviceID := fmt.Sprintf("%s-service", cluster.Name)

	dpuService := &opiv1alpha1.DPUService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-dpuservice", cluster.Name),
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				"opi.io/managed-by": "opi-nvidia-adapter",
				"opi.io/cluster":    cluster.Name,
			},
		},
		Spec: opiv1alpha1.DPUServiceSpec{
			HelmChart:       dpuservicev1.HelmChart{},
			ServiceID:       &serviceID,
			DeployInCluster: &deployInCluster,
		},
	}

	_ = dpuSetName
	return dpuService
}

func boolPtr(value bool) *bool {
	return &value
}
