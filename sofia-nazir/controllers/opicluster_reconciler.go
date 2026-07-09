package controllers

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

	opiv1alpha1 "opi-nvidia-adapter/api/v1alpha1"
	"opi-nvidia-adapter/pkg/adapter"
)

// DPUClusterReconciler reconciles a DPUCluster object
type DPUClusterReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Log      logr.Logger
	Recorder record.EventRecorder
	Adapter  adapter.VendorAdapter
}

// +kubebuilder:rbac:groups=opi.io,resources=dpuclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=opi.io,resources=dpuclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=opi.io,resources=dpuclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=opi.io,resources=dpuseets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=opi.io,resources=dpuseets/status,verbs=get
// +kubebuilder:rbac:groups=opi.io,resources=dpuservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=opi.io,resources=dpuservices/status,verbs=get

func (r *DPUClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("dpucluster", req.NamespacedName)

	var cluster opiv1alpha1.DPUCluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get DPUCluster")
		return ctrl.Result{}, err
	}

	if cluster.Spec.Vendor != "nvidia" {
		log.Info("Skipping DPUCluster: vendor is not nvidia", "vendor", cluster.Spec.Vendor)
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling NVIDIA DPUCluster")

	vendorAdapter := r.Adapter
	if vendorAdapter == nil {
		vendorAdapter = adapter.DefaultVendorAdapter
	}

	desiredDPUSet := vendorAdapter.TranslateDPUSet(&cluster)
	if desiredDPUSet == nil {
		return ctrl.Result{}, nil
	}
	if err := controllerutil.SetControllerReference(&cluster, desiredDPUSet, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference on DPUSet")
		return ctrl.Result{}, err
	}

	existingDPUSet, err := r.reconcileDPUSet(ctx, &cluster, desiredDPUSet, log)
	if err != nil {
		return ctrl.Result{}, err
	}

	desiredDPUService := vendorAdapter.TranslateDPUService(&cluster, existingDPUSet.Name)
	existingDPUService, serviceExists, err := r.reconcileDPUService(ctx, &cluster, desiredDPUService, log)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, &cluster, existingDPUSet, existingDPUService, serviceExists, log); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *DPUClusterReconciler) reconcileDPUSet(ctx context.Context, cluster *opiv1alpha1.DPUCluster, desired *opiv1alpha1.DPUSet, log logr.Logger) (*opiv1alpha1.DPUSet, error) {
	var existing opiv1alpha1.DPUSet
	err := r.Get(ctx, client.ObjectKey{Namespace: desired.Namespace, Name: desired.Name}, &existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Creating a new DPUSet", "Namespace", desired.Namespace, "Name", desired.Name)
			if err := r.Create(ctx, desired); err != nil {
				log.Error(err, "Failed to create DPUSet")
				r.Recorder.Event(cluster, "Warning", "CreationFailed", fmt.Sprintf("Failed to create DPUSet %s", desired.Name))
				return nil, err
			}
			r.Recorder.Event(cluster, "Normal", "Created", fmt.Sprintf("Created DPUSet %s", desired.Name))
			return desired, nil
		}
		log.Error(err, "Failed to get DPUSet")
		return nil, err
	}

	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.Annotations = desired.Annotations
	log.Info("Updating existing DPUSet", "Namespace", existing.Namespace, "Name", existing.Name)
	if err := r.Update(ctx, &existing); err != nil {
		log.Error(err, "Failed to update DPUSet")
		return nil, err
	}

	return &existing, nil
}

func (r *DPUClusterReconciler) reconcileDPUService(ctx context.Context, cluster *opiv1alpha1.DPUCluster, desired *opiv1alpha1.DPUService, log logr.Logger) (*opiv1alpha1.DPUService, bool, error) {
	if desired == nil {
		var existing opiv1alpha1.DPUService
		err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: fmt.Sprintf("%s-dpuservice", cluster.Name)}, &existing)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return nil, false, nil
			}
			return nil, false, err
		}

		log.Info("Deleting old DPUService", "Namespace", existing.Namespace, "Name", existing.Name)
		if err := r.Delete(ctx, &existing); err != nil {
			log.Error(err, "Failed to delete DPUService")
			return nil, true, err
		}
		return nil, true, nil
	}

	if err := controllerutil.SetControllerReference(cluster, desired, r.Scheme); err != nil {
		log.Error(err, "Failed to set controller reference on DPUService")
		return nil, false, err
	}

	var existing opiv1alpha1.DPUService
	err := r.Get(ctx, client.ObjectKey{Namespace: desired.Namespace, Name: desired.Name}, &existing)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("Creating a new DPUService", "Namespace", desired.Namespace, "Name", desired.Name)
			if err := r.Create(ctx, desired); err != nil {
				log.Error(err, "Failed to create DPUService")
				r.Recorder.Event(cluster, "Warning", "CreationFailed", fmt.Sprintf("Failed to create DPUService %s", desired.Name))
				return nil, false, err
			}
			r.Recorder.Event(cluster, "Normal", "Created", fmt.Sprintf("Created DPUService %s", desired.Name))
			return desired, false, nil
		}
		log.Error(err, "Failed to get DPUService")
		return nil, false, err
	}

	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.Annotations = desired.Annotations
	log.Info("Updating existing DPUService", "Namespace", existing.Namespace, "Name", existing.Name)
	if err := r.Update(ctx, &existing); err != nil {
		log.Error(err, "Failed to update DPUService")
		return nil, true, err
	}

	return &existing, true, nil
}

func (r *DPUClusterReconciler) updateStatus(ctx context.Context, cluster *opiv1alpha1.DPUCluster, existingDPUSet *opiv1alpha1.DPUSet, existingDPUService *opiv1alpha1.DPUService, serviceExists bool, log logr.Logger) error {
	var latestDPUSet opiv1alpha1.DPUSet
	if err := r.Get(ctx, client.ObjectKey{Namespace: existingDPUSet.Namespace, Name: existingDPUSet.Name}, &latestDPUSet); err != nil {
		return err
	}

	dpusetReady, dpusetPhase, _ := conditionState(latestDPUSet.Status.Conditions, "Ready")

	serviceReady := true
	if existingDPUService != nil {
		var latestDPUService opiv1alpha1.DPUService
		if err := r.Get(ctx, client.ObjectKey{Namespace: existingDPUService.Namespace, Name: existingDPUService.Name}, &latestDPUService); err != nil {
			return err
		}
		serviceReady, _, _ = conditionState(latestDPUService.Status.Conditions, "Ready")
	} else if serviceExists {
		serviceReady = true
	}

	var newPhase string
	var newReady bool
	var msg string

	if !dpusetReady {
		newReady = false
		if dpusetPhase != "" {
			newPhase = dpusetPhase
		} else {
			newPhase = "DPUProvisioning"
		}
		msg = fmt.Sprintf("Waiting for DPUSet: %s", latestDPUSet.Name)
	} else if !serviceReady {
		newReady = false
		newPhase = "ServiceConfiguring"
		msg = "DPUs provisioned. Waiting for network service config."
	} else {
		newReady = true
		newPhase = "Ready"
		msg = "All DPUs provisioned and services deployed."
	}

	return r.updateConditions(cluster, newReady, newPhase, msg, log)
}

func conditionState(conditions []metav1.Condition, conditionType string) (bool, string, string) {
	for _, condition := range conditions {
		if condition.Type != conditionType {
			continue
		}
		return condition.Status == metav1.ConditionTrue, condition.Reason, condition.Message
	}
	return false, "", ""
}

func (r *DPUClusterReconciler) updateConditions(cluster *opiv1alpha1.DPUCluster, newReady bool, newPhase, msg string, log logr.Logger) error {
	if cluster.Status.Ready == newReady && cluster.Status.Phase == newPhase {
		return nil
	}

	cluster.Status.Ready = newReady
	cluster.Status.Phase = newPhase

	cond := metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		LastTransitionTime: metav1.Now(),
		Reason:             newPhase,
		Message:            msg,
	}
	if newReady {
		cond.Status = metav1.ConditionTrue
	}

	found := false
	for i, c := range cluster.Status.Conditions {
		if c.Type == "Ready" {
			cluster.Status.Conditions[i] = cond
			found = true
			break
		}
	}
	if !found {
		cluster.Status.Conditions = append(cluster.Status.Conditions, cond)
	}

	log.Info("Updating DPUCluster status", "Phase", newPhase, "Ready", newReady)
	if err := r.Status().Update(context.Background(), cluster); err != nil {
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DPUClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opiv1alpha1.DPUCluster{}).
		Owns(&opiv1alpha1.DPUSet{}).
		Owns(&opiv1alpha1.DPUService{}).
		Complete(r)
}
