// Package controller implements the dedicated reconciliation component proposed
// by the OPI-NVIDIA DPF integration architecture (architecture.md).
package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// --- Constants ---

const (
	// VendorNVIDIA identifies NVIDIA-targeted OPI resources (Section 10.1).
	VendorNVIDIA = "nvidia"
	// RequeueDelay is the default requeue interval when prerequisites are unmet.
	RequeueDelay = 10 * time.Second
	// ConditionProvisioning tracks DPU infrastructure lifecycle (Section 5.5.1).
	ConditionProvisioning = "Provisioning"
	// ConditionServiceDeployment tracks service deployment state (Section 5.5.1).
	ConditionServiceDeployment = "ServiceDeployment"
	// ConditionReady aggregates overall readiness (Section 5.5.1).
	ConditionReady = "Ready"
	// ConditionStatusFresh tracks observation recency (Section 5.5.1).
	ConditionStatusFresh = "StatusFresh"
	// DPFConditionReady is the condition type DPF controllers use for readiness.
	DPFConditionReady = "Ready"
)

// --- Placeholder OPI API Types ---
// All placeholder types below must be replaced with real upstream types.
// DeepCopyObject stubs are provided for compilation; use controller-gen in production.

// DpuOperatorConfig is a placeholder for the upstream OPI API type.
type DpuOperatorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DpuOperatorConfigSpec   `json:"spec,omitempty"`
	Status            DpuOperatorConfigStatus `json:"status,omitempty"`
}

// DpuOperatorConfigSpec holds vendor selection and DPF mapping inputs.
type DpuOperatorConfigSpec struct {
	Vendor         string            `json:"vendor"`
	ClusterConfig  map[string]string `json:"clusterConfig,omitempty"`
	ImageConfig    map[string]string `json:"imageConfig,omitempty"`
	HardwareConfig map[string]string `json:"hardwareConfig,omitempty"`
	NodeSelector   map[string]string `json:"nodeSelector,omitempty"`
}

// DpuOperatorConfigStatus carries derived conditions (Section 5.5).
type DpuOperatorConfigStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (in *DpuOperatorConfig) DeepCopyObject() runtime.Object { o := *in; return &o }

// ServiceFunctionChain is a placeholder for the upstream OPI API type.
type ServiceFunctionChain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ServiceFunctionChainSpec   `json:"spec,omitempty"`
	Status            ServiceFunctionChainStatus `json:"status,omitempty"`
}

// ServiceFunctionChainSpec holds service deployment and optional networking intent.
type ServiceFunctionChainSpec struct {
	Vendor           string            `json:"vendor"`
	ServiceConfig    map[string]string `json:"serviceConfig,omitempty"`
	NetworkingConfig *NetworkingConfig `json:"networkingConfig,omitempty"`
}

// NetworkingConfig holds optional DPF networking resource configuration.
type NetworkingConfig struct {
	ChainConfig     map[string]string `json:"chainConfig,omitempty"`
	InterfaceConfig map[string]string `json:"interfaceConfig,omitempty"`
	IPAMConfig      map[string]string `json:"ipamConfig,omitempty"`
}

// ServiceFunctionChainStatus carries derived conditions (Section 5.5).
type ServiceFunctionChainStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (in *ServiceFunctionChain) DeepCopyObject() runtime.Object { o := *in; return &o }

// --- Placeholder DPF API Types ---

// DPUCluster is a placeholder for the upstream DPF API type.
type DPUCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUClusterSpec   `json:"spec,omitempty"`
	Status            DPUClusterStatus `json:"status,omitempty"`
}
type DPUClusterSpec struct {
	Config map[string]string `json:"config,omitempty"`
}
type DPUClusterStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (in *DPUCluster) DeepCopyObject() runtime.Object { o := *in; return &o }

// BFB is a placeholder for the upstream DPF API type.
type BFB struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BFBSpec `json:"spec,omitempty"`
}
type BFBSpec struct {
	Config map[string]string `json:"config,omitempty"`
}

func (in *BFB) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPUFlavor is a placeholder for the upstream DPF API type.
type DPUFlavor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUFlavorSpec `json:"spec,omitempty"`
}
type DPUFlavorSpec struct {
	Config map[string]string `json:"config,omitempty"`
}

func (in *DPUFlavor) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPUSet is a placeholder for the upstream DPF API type.
type DPUSet struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUSetSpec   `json:"spec,omitempty"`
	Status            DPUSetStatus `json:"status,omitempty"`
}
type DPUSetSpec struct {
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	DPUFlavor    string            `json:"dpuFlavor,omitempty"`
	BFB          string            `json:"bfb,omitempty"`
}
type DPUSetStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (in *DPUSet) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPU is a placeholder for the upstream DPF API type. Never created by this controller.
type DPU struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            DPUStatus `json:"status,omitempty"`
}
type DPUStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (in *DPU) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPUList holds a list of DPU resources for readiness observation.
type DPUList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DPU `json:"items"`
}

func (in *DPUList) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPUService is a placeholder for the upstream DPF API type.
type DPUService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUServiceSpec   `json:"spec,omitempty"`
	Status            DPUServiceStatus `json:"status,omitempty"`
}
type DPUServiceSpec struct {
	Config map[string]string `json:"config,omitempty"`
}
type DPUServiceStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (in *DPUService) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPUServiceChain is a placeholder for the upstream DPF API type.
type DPUServiceChain struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUServiceChainSpec `json:"spec,omitempty"`
}
type DPUServiceChainSpec struct {
	Config map[string]string `json:"config,omitempty"`
}

func (in *DPUServiceChain) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPUServiceInterface is a placeholder for the upstream DPF API type.
type DPUServiceInterface struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUServiceInterfaceSpec `json:"spec,omitempty"`
}
type DPUServiceInterfaceSpec struct {
	Config map[string]string `json:"config,omitempty"`
}

func (in *DPUServiceInterface) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPUServiceIPAM is a placeholder for the upstream DPF API type.
type DPUServiceIPAM struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUServiceIPAMSpec `json:"spec,omitempty"`
}
type DPUServiceIPAMSpec struct {
	Config map[string]string `json:"config,omitempty"`
}

func (in *DPUServiceIPAM) DeepCopyObject() runtime.Object { o := *in; return &o }

// DPUServiceCredentialRequest is a placeholder for the upstream DPF API type.
type DPUServiceCredentialRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DPUServiceCredentialRequestSpec   `json:"spec,omitempty"`
	Status            DPUServiceCredentialRequestStatus `json:"status,omitempty"`
}
type DPUServiceCredentialRequestSpec struct {
	ClusterName string `json:"clusterName,omitempty"`
}
type DPUServiceCredentialRequestStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

func (in *DPUServiceCredentialRequest) DeepCopyObject() runtime.Object { o := *in; return &o }

// --- Reconciler Definitions (Section 5.1.3, 10.1) ---

// InfrastructureReconciler reconciles DpuOperatorConfig for NVIDIA infrastructure provisioning.
type InfrastructureReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// ServiceReconciler reconciles ServiceFunctionChain for NVIDIA service deployment.
type ServiceReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// --- SetupWithManager (Section 10.1) ---

// +kubebuilder:rbac:groups=opi.example.com,resources=dpuoperatorconfigs,verbs=get;list;watch
// +kubebuilder:rbac:groups=opi.example.com,resources=dpuoperatorconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpuclusters,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=bfbs,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpuflavors,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpusets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpus,verbs=get;list;watch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpuservicecredentialrequests,verbs=get;list;watch;create;update;patch

// SetupWithManager registers the infrastructure reconciler with the controller manager.
func (r *InfrastructureReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&DpuOperatorConfig{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=opi.example.com,resources=servicefunctionchains,verbs=get;list;watch
// +kubebuilder:rbac:groups=opi.example.com,resources=servicefunctionchains/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpuservices,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpuservicechains,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpuserviceinterfaces,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpuserviceipams,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=dpf.nvidia.com,resources=dpus,verbs=get;list;watch

// SetupWithManager registers the service reconciler with the controller manager.
func (r *ServiceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ServiceFunctionChain{}).
		Complete(r)
}

// --- Reconcile Entry Points (Section 10.3) ---

// Reconcile is the infrastructure reconciliation entry point for DpuOperatorConfig.
func (r *InfrastructureReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Step 1: Observe OPI intent.
	config := &DpuOperatorConfig{}
	if err := r.Get(ctx, req.NamespacedName, config); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("DpuOperatorConfig deleted, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching DpuOperatorConfig: %w", err)
	}

	// Step 2: Filter for NVIDIA vendor only (Section 10.1).
	if !isNVIDIAVendor(config.Spec.Vendor) {
		return ctrl.Result{}, nil
	}

	// Step 3: Validate specification.
	if err := validateDpuOperatorConfigSpec(&config.Spec); err != nil {
		setCondition(&config.Status.Conditions, ConditionReady, metav1.ConditionFalse, "InvalidSpec", err.Error())
		if statusErr := r.Status().Update(ctx, config); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("updating status after validation failure: %w", statusErr)
		}
		return ctrl.Result{}, nil
	}

	// Steps 4-8: Infrastructure reconciliation.
	result, err := r.reconcileInfrastructure(ctx, config)
	if err != nil {
		return result, err
	}

	// Steps 9-10: Derive and propagate status.
	if statusErr := r.deriveAndUpdateInfrastructureStatus(ctx, config); statusErr != nil {
		return ctrl.Result{}, statusErr
	}

	return result, nil
}

// Reconcile is the service reconciliation entry point for ServiceFunctionChain.
func (r *ServiceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Step 1: Observe OPI intent.
	sfc := &ServiceFunctionChain{}
	if err := r.Get(ctx, req.NamespacedName, sfc); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ServiceFunctionChain deleted, skipping reconciliation")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("fetching ServiceFunctionChain: %w", err)
	}

	// Step 2: Filter for NVIDIA vendor only (Section 10.1).
	if !isNVIDIAVendor(sfc.Spec.Vendor) {
		return ctrl.Result{}, nil
	}

	// Step 3: Validate specification.
	if err := validateServiceFunctionChainSpec(&sfc.Spec); err != nil {
		setCondition(&sfc.Status.Conditions, ConditionReady, metav1.ConditionFalse, "InvalidSpec", err.Error())
		if statusErr := r.Status().Update(ctx, sfc); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("updating status after validation failure: %w", statusErr)
		}
		return ctrl.Result{}, nil
	}

	// Steps 4-8: Service reconciliation.
	result, err := r.reconcileServices(ctx, sfc)
	if err != nil {
		return result, err
	}

	// Steps 9-10: Derive and propagate status.
	if statusErr := r.deriveAndUpdateServiceStatus(ctx, sfc); statusErr != nil {
		return ctrl.Result{}, statusErr
	}

	return result, nil
}

// --- Infrastructure Reconciliation (Section 5.3.1, 10.3) ---

// reconcileInfrastructure ensures all DPF infrastructure resources exist per Section 5.3.1.
func (r *InfrastructureReconciler) reconcileInfrastructure(ctx context.Context, config *DpuOperatorConfig) (ctrl.Result, error) {
	// DPUCluster, BFB, DPUFlavor have no readiness dependency (Section 5.3.1).
	cluster, err := r.ensureDPUCluster(ctx, config)
	if err != nil {
		return ctrl.Result{}, err
	}
	bfb, err := r.ensureBFB(ctx, config)
	if err != nil {
		return ctrl.Result{}, err
	}
	flavor, err := r.ensureDPUFlavor(ctx, config)
	if err != nil {
		return ctrl.Result{}, err
	}

	// DPUSet requires DPUCluster ready + BFB exists + DPUFlavor exists.
	if !isDPFResourceReady(cluster.Status.Conditions) {
		log.FromContext(ctx).Info("DPUCluster not ready, requeuing")
		return ctrl.Result{RequeueAfter: RequeueDelay}, nil
	}
	if _, err = r.ensureDPUSet(ctx, config, bfb, flavor); err != nil {
		return ctrl.Result{}, err
	}

	// DPUServiceCredentialRequest requires DPUCluster ready (Section 5.3.3).
	if _, err = r.ensureCredentialRequest(ctx, config, cluster); err != nil {
		return ctrl.Result{}, err
	}

	// DPU objects are never created; only observed (Section 5.3.1 note).
	readyCount, _, err := r.observeDPUReadiness(ctx, config)
	if err != nil {
		return ctrl.Result{}, err
	}
	if readyCount == 0 {
		log.FromContext(ctx).Info("No ready DPUs yet, requeuing")
		return ctrl.Result{RequeueAfter: RequeueDelay}, nil
	}

	return ctrl.Result{}, nil
}

// ensureDPUCluster idempotently ensures a DPUCluster exists for the given config.
func (r *InfrastructureReconciler) ensureDPUCluster(ctx context.Context, config *DpuOperatorConfig) (*DPUCluster, error) {
	desired := buildDesiredDPUCluster(config)
	return ensureResource(ctx, r.Client, desired, func(existing, want *DPUCluster) bool {
		return fmt.Sprintf("%v", existing.Spec.Config) == fmt.Sprintf("%v", want.Spec.Config)
	})
}

// ensureBFB idempotently ensures a BFB exists for the given config.
func (r *InfrastructureReconciler) ensureBFB(ctx context.Context, config *DpuOperatorConfig) (*BFB, error) {
	desired := buildDesiredBFB(config)
	return ensureResource(ctx, r.Client, desired, func(existing, want *BFB) bool {
		return fmt.Sprintf("%v", existing.Spec.Config) == fmt.Sprintf("%v", want.Spec.Config)
	})
}

// ensureDPUFlavor idempotently ensures a DPUFlavor exists for the given config.
func (r *InfrastructureReconciler) ensureDPUFlavor(ctx context.Context, config *DpuOperatorConfig) (*DPUFlavor, error) {
	desired := buildDesiredDPUFlavor(config)
	return ensureResource(ctx, r.Client, desired, func(existing, want *DPUFlavor) bool {
		return fmt.Sprintf("%v", existing.Spec.Config) == fmt.Sprintf("%v", want.Spec.Config)
	})
}

// ensureDPUSet idempotently ensures a DPUSet exists after prerequisites are met.
func (r *InfrastructureReconciler) ensureDPUSet(ctx context.Context, config *DpuOperatorConfig, bfb *BFB, flavor *DPUFlavor) (*DPUSet, error) {
	desired := buildDesiredDPUSet(config, bfb, flavor)
	return ensureResource(ctx, r.Client, desired, func(existing, want *DPUSet) bool {
		return existing.Spec.DPUFlavor == want.Spec.DPUFlavor &&
			existing.Spec.BFB == want.Spec.BFB &&
			fmt.Sprintf("%v", existing.Spec.NodeSelector) == fmt.Sprintf("%v", want.Spec.NodeSelector)
	})
}

// ensureCredentialRequest idempotently ensures a DPUServiceCredentialRequest exists.
func (r *InfrastructureReconciler) ensureCredentialRequest(ctx context.Context, config *DpuOperatorConfig, cluster *DPUCluster) (*DPUServiceCredentialRequest, error) {
	desired := buildDesiredCredentialRequest(config, cluster)
	return ensureResource(ctx, r.Client, desired, func(existing, want *DPUServiceCredentialRequest) bool {
		return existing.Spec.ClusterName == want.Spec.ClusterName
	})
}

// observeDPUReadiness lists DPU resources and counts ready instances. Never creates DPUs.
func (r *InfrastructureReconciler) observeDPUReadiness(ctx context.Context, config *DpuOperatorConfig) (ready int, total int, err error) {
	dpuList := &DPUList{}
	if err = r.List(ctx, dpuList, client.InNamespace(config.Namespace)); err != nil {
		return 0, 0, fmt.Errorf("listing DPUs: %w", err)
	}
	for i := range dpuList.Items {
		if isDPFResourceReady(dpuList.Items[i].Status.Conditions) {
			ready++
		}
	}
	return ready, len(dpuList.Items), nil
}

// --- Service Reconciliation (Section 5.3.2, 10.3) ---

// reconcileServices ensures DPUService and optional networking resources per Section 5.3.2.
func (r *ServiceReconciler) reconcileServices(ctx context.Context, sfc *ServiceFunctionChain) (ctrl.Result, error) {
	// Gate: at least one DPU must be ready before creating DPUService.
	dpuList := &DPUList{}
	if err := r.List(ctx, dpuList, client.InNamespace(sfc.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("listing DPUs: %w", err)
	}
	readyCount := 0
	for i := range dpuList.Items {
		if isDPFResourceReady(dpuList.Items[i].Status.Conditions) {
			readyCount++
		}
	}
	if readyCount == 0 {
		log.FromContext(ctx).Info("No ready DPUs, service deployment gated")
		return ctrl.Result{RequeueAfter: RequeueDelay}, nil
	}

	if _, err := r.ensureDPUService(ctx, sfc); err != nil {
		return ctrl.Result{}, err
	}

	// Optional networking resources, no fixed ordering among them (Section 5.3.2).
	if err := r.ensureNetworkingResources(ctx, sfc); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// ensureDPUService idempotently ensures a DPUService exists for the given SFC.
func (r *ServiceReconciler) ensureDPUService(ctx context.Context, sfc *ServiceFunctionChain) (*DPUService, error) {
	desired := buildDesiredDPUService(sfc)
	return ensureResource(ctx, r.Client, desired, func(existing, want *DPUService) bool {
		return fmt.Sprintf("%v", existing.Spec.Config) == fmt.Sprintf("%v", want.Spec.Config)
	})
}

// ensureNetworkingResources creates optional DPF networking resources when SFC requires them.
func (r *ServiceReconciler) ensureNetworkingResources(ctx context.Context, sfc *ServiceFunctionChain) error {
	if sfc.Spec.NetworkingConfig == nil {
		return nil
	}
	nc := sfc.Spec.NetworkingConfig

	// No fixed dependency ordering among these three resources (Section 5.3.2).
	if len(nc.ChainConfig) > 0 {
		desired := buildDesiredDPUServiceChain(sfc)
		if _, err := ensureResource(ctx, r.Client, desired, func(e, w *DPUServiceChain) bool {
			return fmt.Sprintf("%v", e.Spec.Config) == fmt.Sprintf("%v", w.Spec.Config)
		}); err != nil {
			return err
		}
	}
	if len(nc.InterfaceConfig) > 0 {
		desired := buildDesiredDPUServiceInterface(sfc)
		if _, err := ensureResource(ctx, r.Client, desired, func(e, w *DPUServiceInterface) bool {
			return fmt.Sprintf("%v", e.Spec.Config) == fmt.Sprintf("%v", w.Spec.Config)
		}); err != nil {
			return err
		}
	}
	if len(nc.IPAMConfig) > 0 {
		desired := buildDesiredDPUServiceIPAM(sfc)
		if _, err := ensureResource(ctx, r.Client, desired, func(e, w *DPUServiceIPAM) bool {
			return fmt.Sprintf("%v", e.Spec.Config) == fmt.Sprintf("%v", w.Spec.Config)
		}); err != nil {
			return err
		}
	}
	return nil
}

// --- Status Propagation (Section 5.5, 10.5) ---
// Status is implemented using metav1.Condition. This is one valid implementation
// of Section 5.5.2; the architecture does not mandate this specific schema.

// deriveAndUpdateInfrastructureStatus derives provisioning status from DPF resources.
func (r *InfrastructureReconciler) deriveAndUpdateInfrastructureStatus(ctx context.Context, config *DpuOperatorConfig) error {
	provStatus, provReason, provMsg := r.deriveProvisioningState(ctx, config)
	setCondition(&config.Status.Conditions, ConditionProvisioning, provStatus, provReason, provMsg)
	setCondition(&config.Status.Conditions, ConditionStatusFresh, metav1.ConditionTrue, "Observed", fmt.Sprintf("Status observed at %s", time.Now().UTC().Format(time.RFC3339)))

	// Overall readiness: true only when provisioning succeeds.
	if provStatus == metav1.ConditionTrue {
		setCondition(&config.Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready", "Infrastructure provisioning complete")
	} else {
		setCondition(&config.Status.Conditions, ConditionReady, metav1.ConditionFalse, "NotReady", provMsg)
	}

	if err := r.Status().Update(ctx, config); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("optimistic concurrency conflict updating status: %w", err)
		}
		return fmt.Errorf("updating DpuOperatorConfig status: %w", err)
	}
	return nil
}

// deriveProvisioningState reads DPUCluster and DPU status to determine provisioning state.
func (r *InfrastructureReconciler) deriveProvisioningState(ctx context.Context, config *DpuOperatorConfig) (metav1.ConditionStatus, string, string) {
	cluster := &DPUCluster{}
	clusterKey := types.NamespacedName{Name: resourceName(config.Name, "dpucluster"), Namespace: config.Namespace}
	if err := r.Get(ctx, clusterKey, cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return metav1.ConditionFalse, "DPUClusterMissing", "DPUCluster has not been created yet"
		}
		return metav1.ConditionUnknown, "ObservationFailed", "Unable to observe DPUCluster status"
	}
	if !isDPFResourceReady(cluster.Status.Conditions) {
		return metav1.ConditionFalse, "DPUClusterNotReady", "DPUCluster is not yet ready"
	}

	readyDPUs, totalDPUs, err := r.observeDPUReadiness(ctx, config)
	if err != nil {
		return metav1.ConditionUnknown, "ObservationFailed", "Unable to observe DPU status"
	}
	if totalDPUs == 0 {
		return metav1.ConditionFalse, "NoDPUs", "No DPU resources exist yet"
	}
	if readyDPUs == 0 {
		return metav1.ConditionFalse, "NoDPUsReady", fmt.Sprintf("0/%d DPUs ready", totalDPUs)
	}
	return metav1.ConditionTrue, "Provisioned", fmt.Sprintf("%d/%d DPUs ready", readyDPUs, totalDPUs)
}

// deriveAndUpdateServiceStatus derives service deployment status from DPF resources.
func (r *ServiceReconciler) deriveAndUpdateServiceStatus(ctx context.Context, sfc *ServiceFunctionChain) error {
	svcStatus, svcReason, svcMsg := r.deriveServiceDeploymentState(ctx, sfc)
	setCondition(&sfc.Status.Conditions, ConditionServiceDeployment, svcStatus, svcReason, svcMsg)
	setCondition(&sfc.Status.Conditions, ConditionStatusFresh, metav1.ConditionTrue, "Observed", fmt.Sprintf("Status observed at %s", time.Now().UTC().Format(time.RFC3339)))

	if svcStatus == metav1.ConditionTrue {
		setCondition(&sfc.Status.Conditions, ConditionReady, metav1.ConditionTrue, "Ready", "Service deployment complete")
	} else {
		setCondition(&sfc.Status.Conditions, ConditionReady, metav1.ConditionFalse, "NotReady", svcMsg)
	}

	if err := r.Status().Update(ctx, sfc); err != nil {
		if apierrors.IsConflict(err) {
			return fmt.Errorf("optimistic concurrency conflict updating status: %w", err)
		}
		return fmt.Errorf("updating ServiceFunctionChain status: %w", err)
	}
	return nil
}

// deriveServiceDeploymentState reads DPUService status to determine deployment state.
func (r *ServiceReconciler) deriveServiceDeploymentState(ctx context.Context, sfc *ServiceFunctionChain) (metav1.ConditionStatus, string, string) {
	svc := &DPUService{}
	svcKey := types.NamespacedName{Name: resourceName(sfc.Name, "dpuservice"), Namespace: sfc.Namespace}
	if err := r.Get(ctx, svcKey, svc); err != nil {
		if apierrors.IsNotFound(err) {
			return metav1.ConditionFalse, "DPUServiceMissing", "DPUService has not been created yet"
		}
		return metav1.ConditionUnknown, "ObservationFailed", "Unable to observe DPUService status"
	}
	if isDPFResourceFailed(svc.Status.Conditions) {
		return metav1.ConditionFalse, "DeploymentFailed", "DPUService reports a failed state"
	}
	if !isDPFResourceReady(svc.Status.Conditions) {
		return metav1.ConditionFalse, "DeploymentInProgress", "DPUService is not yet ready"
	}
	return metav1.ConditionTrue, "Deployed", "DPUService reports ready"
}

// --- Validation Helpers (Section 10.3 step 3) ---

// isNVIDIAVendor returns true if the vendor string identifies NVIDIA hardware.
func isNVIDIAVendor(vendor string) bool {
	return vendor == VendorNVIDIA
}

// validateDpuOperatorConfigSpec validates the minimum required fields for infrastructure reconciliation.
func validateDpuOperatorConfigSpec(spec *DpuOperatorConfigSpec) error {
	if spec.Vendor == "" {
		return fmt.Errorf("spec.vendor is required")
	}
	if len(spec.ClusterConfig) == 0 {
		return fmt.Errorf("spec.clusterConfig is required for DPUCluster mapping")
	}
	if len(spec.ImageConfig) == 0 {
		return fmt.Errorf("spec.imageConfig is required for BFB mapping")
	}
	if len(spec.HardwareConfig) == 0 {
		return fmt.Errorf("spec.hardwareConfig is required for DPUFlavor mapping")
	}
	return nil
}

// validateServiceFunctionChainSpec validates the minimum required fields for service reconciliation.
func validateServiceFunctionChainSpec(spec *ServiceFunctionChainSpec) error {
	if spec.Vendor == "" {
		return fmt.Errorf("spec.vendor is required")
	}
	if len(spec.ServiceConfig) == 0 {
		return fmt.Errorf("spec.serviceConfig is required for DPUService mapping")
	}
	return nil
}

// --- Resource Builders (Section 5.3) ---

// buildDesiredDPUCluster maps DpuOperatorConfig cluster intent to a DPUCluster.
func buildDesiredDPUCluster(config *DpuOperatorConfig) *DPUCluster {
	return &DPUCluster{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(config.Name, "dpucluster"), Namespace: config.Namespace},
		Spec:       DPUClusterSpec{Config: config.Spec.ClusterConfig},
	}
}

// buildDesiredBFB maps DpuOperatorConfig image intent to a BFB.
func buildDesiredBFB(config *DpuOperatorConfig) *BFB {
	return &BFB{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(config.Name, "bfb"), Namespace: config.Namespace},
		Spec:       BFBSpec{Config: config.Spec.ImageConfig},
	}
}

// buildDesiredDPUFlavor maps DpuOperatorConfig hardware intent to a DPUFlavor.
func buildDesiredDPUFlavor(config *DpuOperatorConfig) *DPUFlavor {
	return &DPUFlavor{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(config.Name, "dpuflavor"), Namespace: config.Namespace},
		Spec:       DPUFlavorSpec{Config: config.Spec.HardwareConfig},
	}
}

// buildDesiredDPUSet maps DpuOperatorConfig node selection intent to a DPUSet.
func buildDesiredDPUSet(config *DpuOperatorConfig, bfb *BFB, flavor *DPUFlavor) *DPUSet {
	return &DPUSet{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(config.Name, "dpuset"), Namespace: config.Namespace},
		Spec: DPUSetSpec{
			NodeSelector: config.Spec.NodeSelector,
			BFB:          bfb.Name,
			DPUFlavor:    flavor.Name,
		},
	}
}

// buildDesiredCredentialRequest maps cross-cluster credential intent.
func buildDesiredCredentialRequest(config *DpuOperatorConfig, cluster *DPUCluster) *DPUServiceCredentialRequest {
	return &DPUServiceCredentialRequest{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(config.Name, "credreq"), Namespace: config.Namespace},
		Spec:       DPUServiceCredentialRequestSpec{ClusterName: cluster.Name},
	}
}

// buildDesiredDPUService maps ServiceFunctionChain service intent to a DPUService.
func buildDesiredDPUService(sfc *ServiceFunctionChain) *DPUService {
	return &DPUService{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(sfc.Name, "dpuservice"), Namespace: sfc.Namespace},
		Spec:       DPUServiceSpec{Config: sfc.Spec.ServiceConfig},
	}
}

// buildDesiredDPUServiceChain maps SFC networking chain intent.
func buildDesiredDPUServiceChain(sfc *ServiceFunctionChain) *DPUServiceChain {
	return &DPUServiceChain{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(sfc.Name, "dpuservicechain"), Namespace: sfc.Namespace},
		Spec:       DPUServiceChainSpec{Config: sfc.Spec.NetworkingConfig.ChainConfig},
	}
}

// buildDesiredDPUServiceInterface maps SFC networking interface intent.
func buildDesiredDPUServiceInterface(sfc *ServiceFunctionChain) *DPUServiceInterface {
	return &DPUServiceInterface{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(sfc.Name, "dpuserviceiface"), Namespace: sfc.Namespace},
		Spec:       DPUServiceInterfaceSpec{Config: sfc.Spec.NetworkingConfig.InterfaceConfig},
	}
}

// buildDesiredDPUServiceIPAM maps SFC networking IPAM intent.
func buildDesiredDPUServiceIPAM(sfc *ServiceFunctionChain) *DPUServiceIPAM {
	return &DPUServiceIPAM{
		ObjectMeta: metav1.ObjectMeta{Name: resourceName(sfc.Name, "dpuserviceipam"), Namespace: sfc.Namespace},
		Spec:       DPUServiceIPAMSpec{Config: sfc.Spec.NetworkingConfig.IPAMConfig},
	}
}

// --- Generic Resource Helper ---

// ensureResource idempotently ensures a DPF resource exists, creating or updating as needed.
// Handles: NotFound, transient API errors, optimistic concurrency conflicts, idempotent reconciliation.
func ensureResource[T client.Object](ctx context.Context, c client.Client, desired T, specMatches func(existing, desired T) bool) (T, error) {
	key := types.NamespacedName{Name: desired.GetName(), Namespace: desired.GetNamespace()}
	existing := desired.DeepCopyObject().(T)
	err := c.Get(ctx, key, existing)
	if apierrors.IsNotFound(err) {
		if createErr := c.Create(ctx, desired); createErr != nil {
			if apierrors.IsAlreadyExists(createErr) {
				// Handle duplicate watch events; re-fetch and return.
				if getErr := c.Get(ctx, key, existing); getErr != nil {
					return existing, fmt.Errorf("re-fetching after AlreadyExists: %w", getErr)
				}
				return existing, nil
			}
			return desired, fmt.Errorf("creating %s/%s: %w", key.Namespace, key.Name, createErr)
		}
		return desired, nil
	}
	if err != nil {
		return existing, fmt.Errorf("fetching %s/%s: %w", key.Namespace, key.Name, err)
	}
	// Resource exists; update only if spec diverges (idempotent, Section 5.4.5).
	if !specMatches(existing, desired) {
		desired.SetResourceVersion(existing.GetResourceVersion())
		if updateErr := c.Update(ctx, desired); updateErr != nil {
			return existing, fmt.Errorf("updating %s/%s: %w", key.Namespace, key.Name, updateErr)
		}
		return desired, nil
	}
	return existing, nil
}

// --- Utility Helpers ---

// isDPFResourceReady checks whether a DPF resource reports a Ready condition.
func isDPFResourceReady(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == DPFConditionReady && c.Status == metav1.ConditionTrue {
			return true
		}
	}
	return false
}

// isDPFResourceFailed checks whether a DPF resource reports a Failed condition.
func isDPFResourceFailed(conditions []metav1.Condition) bool {
	for _, c := range conditions {
		if c.Type == DPFConditionReady && c.Status == metav1.ConditionFalse && c.Reason == "Failed" {
			return true
		}
	}
	return false
}

// setCondition sets or updates a metav1.Condition in the given slice.
func setCondition(conditions *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i, c := range *conditions {
		if c.Type == condType {
			if c.Status != status || c.Reason != reason || c.Message != message {
				(*conditions)[i] = metav1.Condition{
					Type: condType, Status: status, Reason: reason,
					Message: message, LastTransitionTime: now,
				}
			}
			return
		}
	}
	*conditions = append(*conditions, metav1.Condition{
		Type: condType, Status: status, Reason: reason,
		Message: message, LastTransitionTime: now,
	})
}

// resourceName produces a deterministic DPF resource name from the OPI resource name.
func resourceName(opiName, suffix string) string {
	return fmt.Sprintf("%s-%s", opiName, suffix)
}

// --- Open Architectural Gaps (Section 7) ---
//
// The following gaps are documented but intentionally not implemented:
// - OPI resource deletion during DPF reconciliation: cleanup strategy (owner references
//   or finalizers) is left to future implementation (Section 7, failure mode #3).
// - Overlapping OPI resources targeting the same physical DPU: no conflict detection
//   or resolution mechanism is defined (Section 7, failure mode #5).
// - Manual modification of DPF resources created by this controller is outside the
//   supported operational model (Section 5.4.6).
// - Permanent loss of the DPF control plane is outside architectural scope (Section 5.4.6).
