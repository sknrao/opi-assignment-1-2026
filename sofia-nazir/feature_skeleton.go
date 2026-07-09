//go:build skeleton

package main

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// ============================================================================
// API Schema Definitions
// ============================================================================

// DPUClusterSpec defines the desired state of DPUCluster
type DPUClusterSpec struct {
	Vendor             string            `json:"vendor"`
	NetworkOffloadMode string            `json:"networkOffloadMode,omitempty"`
	BFB                string            `json:"bfb,omitempty"`
	DpuFlavor          string            `json:"dpuFlavor,omitempty"`
	NodeSelector       map[string]string `json:"nodeSelector,omitempty"`
	VpcName            string            `json:"vpcName,omitempty"`
}

// DPUClusterStatus defines the observed state of DPUCluster
type DPUClusterStatus struct {
	Ready      bool               `json:"ready"`
	Phase      string             `json:"phase,omitempty"`
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

// DPUSetSpec defines the desired state of DPUSet (NVIDIA DPF CRD)
type DPUSetSpec struct {
	DpuNodeSelector map[string]string `json:"dpuNodeSelector"`
	BFB             string            `json:"bfb"`
	Flavor          string            `json:"flavor,omitempty"`
}

// DPUSetStatus defines the observed state of DPUSet
type DPUSetStatus struct {
	Ready   bool   `json:"ready"`
	Phase   string `json:"phase,omitempty"`
	Message string `json:"message,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DPUSet is the Schema for the dpuseets API
type DPUSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DPUSetSpec   `json:"spec,omitempty"`
	Status DPUSetStatus `json:"status,omitempty"`
}

// DPUServiceSpec defines the desired state of DPUService (NVIDIA DPF CRD)
type DPUServiceSpec struct {
	ServiceType string `json:"serviceType"`
	Config      string `json:"config,omitempty"`
	DPUSetName  string `json:"dpuSetName"`
}

// DPUServiceStatus defines the observed state of DPUService
type DPUServiceStatus struct {
	Ready          bool `json:"ready"`
	ActiveServices int  `json:"activeServices"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DPUService is the Schema for the dpuservices API
type DPUService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DPUServiceSpec   `json:"spec,omitempty"`
	Status DPUServiceStatus `json:"status,omitempty"`
}

// ============================================================================
// DeepCopy Implementations for Runtime Objects
// ============================================================================

func (in *DPUCluster) DeepCopyInto(out *DPUCluster) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	in.Status.DeepCopyInto(&out.Status)
}

func (in *DPUCluster) DeepCopy() *DPUCluster {
	if in == nil {
		return nil
	}
	out := new(DPUCluster)
	in.DeepCopyInto(out)
	return out
}

func (in *DPUCluster) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

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

func (in *DPUSet) DeepCopyInto(out *DPUSet) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	in.Spec.DeepCopyInto(&out.Spec)
	out.Status = in.Status
}

func (in *DPUSet) DeepCopy() *DPUSet {
	if in == nil {
		return nil
	}
	out := new(DPUSet)
	in.DeepCopyInto(out)
	return out
}

func (in *DPUSet) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

func (in *DPUSetSpec) DeepCopyInto(out *DPUSetSpec) {
	*out = *in
	if in.DpuNodeSelector != nil {
		in, out := &in.DpuNodeSelector, &out.DpuNodeSelector
		*out = make(map[string]string, len(*in))
		for key, val := range *in {
			(*out)[key] = val
		}
	}
}

func (in *DPUService) DeepCopyInto(out *DPUService) {
	*out = *in
	out.TypeMeta = in.TypeMeta
	in.ObjectMeta.DeepCopyInto(&out.ObjectMeta)
	out.Spec = in.Spec
	out.Status = in.Status
}

func (in *DPUService) DeepCopy() *DPUService {
	if in == nil {
		return nil
	}
	out := new(DPUService)
	in.DeepCopyInto(out)
	return out
}

func (in *DPUService) DeepCopyObject() runtime.Object {
	if c := in.DeepCopy(); c != nil {
		return c
	}
	return nil
}

// ============================================================================
// Translator Logic
// ============================================================================

// TranslateDPUClusterToDPUSet maps an OPI DPUCluster to a DPF DPUSet
func TranslateDPUClusterToDPUSet(cluster *DPUCluster) *DPUSet {
	if cluster == nil {
		return nil
	}
	flavor := cluster.Spec.DpuFlavor
	if flavor == "" {
		flavor = "default"
	}
	return &DPUSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-dpuset", cluster.Name),
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				"opi.io/managed-by": "opi-nvidia-adapter",
				"opi.io/cluster":    cluster.Name,
			},
		},
		Spec: DPUSetSpec{
			DpuNodeSelector: cluster.Spec.NodeSelector,
			BFB:             cluster.Spec.BFB,
			Flavor:          flavor,
		},
	}
}

// TranslateDPUClusterToDPUService maps an OPI DPUCluster to a DPF DPUService
func TranslateDPUClusterToDPUService(cluster *DPUCluster, dpuSetName string) *DPUService {
	if cluster == nil || cluster.Spec.NetworkOffloadMode == "" {
		return nil
	}
	configStr := fmt.Sprintf(`{"mode": "%s", "vpc": "%s"}`, cluster.Spec.NetworkOffloadMode, cluster.Spec.VpcName)
	return &DPUService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-dpuservice", cluster.Name),
			Namespace: cluster.Namespace,
			Labels: map[string]string{
				"opi.io/managed-by": "opi-nvidia-adapter",
				"opi.io/cluster":    cluster.Name,
			},
		},
		Spec: DPUServiceSpec{
			ServiceType: "network",
			Config:      configStr,
			DPUSetName:  dpuSetName,
		},
	}
}

// ============================================================================
// Controller Reconciler
// ============================================================================

// DPUClusterReconciler reconciles a DPUCluster object by adapting to NVIDIA DPF CRDs
type DPUClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Log      logr.Logger
	Recorder record.EventRecorder
}

func (r *DPUClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("dpucluster", req.NamespacedName)

	// Fetch DPUCluster
	var cluster DPUCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Filter for NVIDIA vendor
	if cluster.Spec.Vendor != "nvidia" {
		log.Info("Skipping non-NVIDIA DPUCluster", "vendor", cluster.Spec.Vendor)
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling NVIDIA DPUCluster adapter")

	// 1. Reconcile DPUSet
	desiredDPUSet := TranslateDPUClusterToDPUSet(&cluster)
	if err := controllerutil.SetControllerReference(&cluster, desiredDPUSet, r.Scheme); err != nil {
		return ctrl.Result{}, err
	}

	var existingDPUSet DPUSet
	err := r.Get(ctx, client.ObjectKey{Namespace: desiredDPUSet.Namespace, Name: desiredDPUSet.Name}, &existingDPUSet)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Creating DPUSet", "name", desiredDPUSet.Name)
			if err := r.Create(ctx, desiredDPUSet); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			return ctrl.Result{}, err
		}
	} else {
		// Update specs if changed
		if existingDPUSet.Spec.BFB != desiredDPUSet.Spec.BFB || existingDPUSet.Spec.Flavor != desiredDPUSet.Spec.Flavor {
			existingDPUSet.Spec.BFB = desiredDPUSet.Spec.BFB
			existingDPUSet.Spec.Flavor = desiredDPUSet.Spec.Flavor
			existingDPUSet.Spec.DpuNodeSelector = desiredDPUSet.Spec.DpuNodeSelector
			log.Info("Updating DPUSet spec", "name", existingDPUSet.Name)
			if err := r.Update(ctx, &existingDPUSet); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// 2. Reconcile DPUService
	desiredDPUService := TranslateDPUClusterToDPUService(&cluster, desiredDPUSet.Name)
	var existingDPUService DPUService
	serviceExists := false

	if desiredDPUService != nil {
		if err := controllerutil.SetControllerReference(&cluster, desiredDPUService, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		err = r.Get(ctx, client.ObjectKey{Namespace: desiredDPUService.Namespace, Name: desiredDPUService.Name}, &existingDPUService)
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("Creating DPUService", "name", desiredDPUService.Name)
				if err := r.Create(ctx, desiredDPUService); err != nil {
					return ctrl.Result{}, err
				}
			} else {
				return ctrl.Result{}, err
			}
		} else {
			serviceExists = true
			if existingDPUService.Spec.Config != desiredDPUService.Spec.Config {
				existingDPUService.Spec.Config = desiredDPUService.Spec.Config
				log.Info("Updating DPUService spec", "name", existingDPUService.Name)
				if err := r.Update(ctx, &existingDPUService); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
	}

	// 3. Status Propagation
	err = r.Get(ctx, client.ObjectKey{Namespace: desiredDPUSet.Namespace, Name: desiredDPUSet.Name}, &existingDPUSet)
	if err != nil {
		return ctrl.Result{}, err
	}

	dpusetReady := existingDPUSet.Status.Ready
	dpusetPhase := existingDPUSet.Status.Phase

	serviceReady := true
	if desiredDPUService != nil {
		err = r.Get(ctx, client.ObjectKey{Namespace: desiredDPUService.Namespace, Name: desiredDPUService.Name}, &existingDPUService)
		if err != nil {
			return ctrl.Result{}, err
		}
		serviceReady = existingDPUService.Status.Ready
	} else if serviceExists {
		// Clean up disabled service
		if err := r.Delete(ctx, &existingDPUService); err != nil {
			return ctrl.Result{}, err
		}
	}

	var newPhase string
	var newReady bool
	if !dpusetReady {
		newReady = false
		newPhase = dpusetPhase
		if newPhase == "" {
			newPhase = "DPUProvisioning"
		}
	} else if !serviceReady {
		newReady = false
		newPhase = "ServiceConfiguring"
	} else {
		newReady = true
		newPhase = "Ready"
	}

	if cluster.Status.Ready != newReady || cluster.Status.Phase != newPhase {
		cluster.Status.Ready = newReady
		cluster.Status.Phase = newPhase
		log.Info("Updating DPUCluster status", "Phase", newPhase, "Ready", newReady)
		if err := r.Status().Update(ctx, &cluster); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func main() {
	fmt.Println("OPI DPU Operator NVIDIA Support Adapter Skeleton compiled successfully!")
}
