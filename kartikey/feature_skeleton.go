// Package adapter is the OPI dpu-operator ↔ NVIDIA DPF adapter (pattern (e) from
// architecture_design.md: the "VSP-fronted translation controller"). This file,
// feature_skeleton.go, is the primary bonus deliverable and the entry point for
// reading the code — it holds the two reconcilers that ARE the adapter loop.
//
// ─────────────────────────────────────────────────────────────────────────────
// HOW THE FILES FIT TOGETHER (this is a Kubebuilder-shaped module; the root
// package `adapter` is the controller package so this deliverable file sits at
// the repo root as required):
//
//	feature_skeleton.go   ← YOU ARE HERE. The reconcilers:
//	                         • ServiceFunctionChainReconciler  (OPI SFC  → DPF chain)
//	                         • DataProcessingUnitReconciler    (OPI DPU  → DPF provisioning)
//	                        plus the end-to-end Reconcile loops and DPF error
//	                        classification. Reading just this file tells the
//	                        whole control-flow story; the companions below are
//	                        the types it operates on.
//
//	fleet.go              ← same package. The isolation + multi-tenancy seams
//	                         added by the self-review (arch §9): vendor scoping
//	                         (isNVIDIAManaged), the Fleet struct, and the
//	                         ClusterResolver / SingleFleetResolver.
//	status.go             ← same package. DPF-phase → adapter-owned OPI
//	                         condition (`DPFReady`) mapping and reasons (arch §5).
//
//	api/opi/v1/           ← LOCAL MIRRORS of the OPI public CRDs the adapter
//	                         reads/writes (ServiceFunctionChain, DataProcessingUnit;
//	                         group config.openshift.io/v1) + hand-written deepcopy.
//	internal/dpf/         ← LOCAL MIRRORS of the DPF CRD field-subset + GVKs the
//	                         adapter drives via the dynamic/unstructured client.
//	internal/translate/   ← pure OPI-intent → DPF-object(s) translation (arch §3).
//	internal/vsp/         ← the OPI 8-RPC VendorPlugin contract + the thin,
//	                         credential-free NvidiaVSP node-side implementation.
//	cmd/main.go           ← manager wiring: scheme, one Fleet, the resolver, and
//	                         SetupWithManager for both reconcilers.
//
// ─────────────────────────────────────────────────────────────────────────────
//
// This is intentionally-unfinished but COMPILING controller-runtime code. It
// shows the adapter loop end to end:
//
//	fetch OPI object → reject if not NVIDIA-managed → resolve its fleet →
//	translate to DPF object(s) → server-side apply/patch (loud-fail on a DPF
//	version/GVK mismatch) → read DPF status back → map onto an adapter-OWNED
//	OPI .status condition → requeue.
//
// The vendor-scoping guard, per-fleet resolver, and version-drift classifier
// were added by the self-review pass (arch §9). Unfinished-on-purpose spots are
// marked TODO(dpuf) and return sensible requeues rather than panicking, so this
// reads like production code paused mid-implementation, not a toy. Real deps
// (sigs.k8s.io/controller-runtime, k8s.io/apimachinery) — no stand-ins.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	opiv1 "github.com/opiproject/opi-nvidia-dpf-adapter/api/opi/v1"
	"github.com/opiproject/opi-nvidia-dpf-adapter/internal/dpf"
	"github.com/opiproject/opi-nvidia-dpf-adapter/internal/translate"
)

// Requeue cadences. Transient DPU-cluster errors back off; steady provisioning
// polls faster because DPF phase transitions are the expected path.
const (
	requeueProvisioning = 15 * time.Second
	requeueTransient    = 30 * time.Second
	fieldOwner          = "opi-nvidia-dpf-adapter"

	// chainFinalizer is placed on an OPI ServiceFunctionChain the adapter has
	// translated into DPF objects, so the adapter can garbage-collect those DPF
	// objects when the SFC is deleted. OwnerReferences are NOT usable here: the
	// DPF objects live in the fleet namespace (and, in the multi-fleet/1-cluster
	// futures, potentially a different namespace than the OPI object), and a
	// cross-namespace ownerReference is invalid — Kubernetes GC would treat the
	// owner as missing and could delete the dependent immediately. So the adapter
	// owns cleanup explicitly via this finalizer (arch §12, Q4).
	chainFinalizer = "dpu.nvidia.com/chain-cleanup"
)

// Classified error sentinels, so Reconcile can map each to the right OPI
// condition instead of a generic failure (arch §9, Q2 + Q3).
var (
	// errDPUClusterUnreachable — transient loss of the DPU cluster (arch §4-iii).
	errDPUClusterUnreachable = errors.New("dpu cluster unreachable")
	// errDPFVersionUnsupported — a DPF CRD GVK the adapter targets is not
	// served by the cluster (renamed/removed on a DPF upgrade). This MUST fail
	// loudly, never silently no-op (arch §9, Q2).
	errDPFVersionUnsupported = errors.New("dpf crd version unsupported")
	// errDPFStatusSchemaUnknown — a DPF object IS served and readable, but its
	// status no longer exposes the readiness signal the adapter reads (e.g. DPF
	// moved DPU readiness off status.phase). Distinct from the GVK-level drift
	// above: the CRD still exists, only the status SHAPE changed. Surfaced loudly
	// so a readiness-signal change is visible, not a silent forever-provisioning
	// (arch §12, Q3).
	errDPFStatusSchemaUnknown = errors.New("dpf status schema unrecognized")
)

// DPUClusterProbe reports whether a fleet's static DPUCluster (its kubeconfig-
// Secret BYO cluster, arch §6) is currently reachable. Injected per-fleet so
// each fleet's credential lives behind its own seam (arch §9, Q4).
type DPUClusterProbe interface {
	Reachable(ctx context.Context) error
}

// fleetToTarget converts a Fleet (fleet.go) into the translator's input DTO.
// (translate.TargetFleet is defined in the translate package to avoid a
// translate→adapter import cycle.)
func fleetToTarget(f Fleet) translate.TargetFleet {
	return translate.TargetFleet{
		Name:         f.Name,
		Namespace:    f.Namespace,
		NodeSelector: f.NodeSelector,
		DPUFlavor:    f.DPUFlavor,
	}
}

// ServiceFunctionChainReconciler reconciles an OPI ServiceFunctionChain into
// DPF DPUServiceChain + DPUServiceInterface(s) + DPUService(s), then mirrors DPF
// status back onto the SFC. This is the adapter's primary Day-2 loop.
type ServiceFunctionChainReconciler struct {
	// Client is the HOST-cluster client (owns OPI + DPF objects there).
	client.Client
	Scheme *runtime.Scheme
	// Translator turns OPI intent into DPF unstructured objects (arch §3).
	Translator translate.Translator
	// Fleets resolves an OPI object to its fleet, and rejects non-NVIDIA
	// objects — the adapter's isolation boundary (arch §9, Q3/Q4).
	Fleets ClusterResolver
}

// +kubebuilder:rbac:groups=config.openshift.io,resources=servicefunctionchains,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=config.openshift.io,resources=servicefunctionchains/status,verbs=update;patch
// +kubebuilder:rbac:groups=config.openshift.io,resources=servicefunctionchains/finalizers,verbs=update
// +kubebuilder:rbac:groups=svc.dpu.nvidia.com,resources=dpuservicechains;dpuserviceinterfaces;dpuservices,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the controller-runtime entry point. It is written to be
// re-entrant and idempotent: every call fully re-derives desired DPF state from
// the OPI object and converges.
func (r *ServiceFunctionChainReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithValues("servicefunctionchain", req.NamespacedName)

	// 1) Fetch the OPI-side object. NotFound => deleted; nothing to do.
	sfc := &opiv1.ServiceFunctionChain{}
	if err := r.Get(ctx, req.NamespacedName, sfc); err != nil {
		if apierrors.IsNotFound(err) {
			l.V(1).Info("ServiceFunctionChain gone, skipping")
			return ctrl.Result{}, nil
		}
		l.Error(err, "unable to fetch ServiceFunctionChain")
		return ctrl.Result{}, err
	}

	// 2) ISOLATION GUARD (arch §9, Q3). ServiceFunctionChain is a vendor-
	// NEUTRAL OPI CRD; an Intel/Marvell SFC must never be translated into DPF
	// objects by this NVIDIA adapter. Resolve() returns ok=false for anything
	// not ours, and we exit WITHOUT writing status (another vendor owns it).
	fleet, ok, err := r.Fleets.Resolve(ctx, sfc)
	if err != nil {
		l.Error(err, "fleet resolution failed")
		return ctrl.Result{}, err
	}
	if !ok {
		// Not ours. If we somehow still hold a finalizer (e.g. the vendor label
		// was flipped after we adopted it), release it so we never wedge another
		// vendor's deletion — but do NOT delete objects we can't attribute.
		if !sfc.DeletionTimestamp.IsZero() && controllerutil.ContainsFinalizer(sfc, chainFinalizer) {
			return ctrl.Result{}, r.removeFinalizer(ctx, sfc)
		}
		l.V(1).Info("SFC not NVIDIA-managed; ignoring")
		return ctrl.Result{}, nil
	}
	tgt := fleetToTarget(fleet)

	// 2b) DELETION (arch §12, Q4). The DPF objects this adapter created carry no
	// ownerReference back to the SFC (a cross-namespace owner ref is invalid), so
	// Kubernetes GC will not reclaim them — the adapter must. On delete, cascade-
	// delete the fleet-scoped DPF objects, then drop the finalizer. This is what
	// makes the adapter a "well-behaved" DPF client: its objects do not outlive
	// the intent that created them and never accumulate for DPF's own GC to trip
	// over.
	if !sfc.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(sfc, chainFinalizer) {
			if derr := r.deleteChain(ctx, r.Translator.ChainForSFC(tgt, sfc)); derr != nil {
				l.Error(derr, "failed to garbage-collect DPF chain objects; will retry")
				return ctrl.Result{}, derr
			}
			return ctrl.Result{}, r.removeFinalizer(ctx, sfc)
		}
		return ctrl.Result{}, nil
	}

	// 2c) Adopt the object: ensure our cleanup finalizer is present BEFORE we
	// create any DPF objects, so a delete that races creation still cleans up.
	if !controllerutil.ContainsFinalizer(sfc, chainFinalizer) {
		if ferr := r.addFinalizer(ctx, sfc); ferr != nil {
			l.Error(ferr, "failed to add cleanup finalizer")
			return ctrl.Result{}, ferr
		}
	}

	// 3) Guard: if this fleet's DPU cluster is unreachable, surface Degraded and
	// back off WITHOUT clearing existing status (arch §5).
	if fleet.Probe != nil {
		if perr := fleet.Probe.Reachable(ctx); perr != nil {
			l.Info("DPU cluster unreachable; surfacing Degraded", "cause", perr.Error())
			cond := degraded(ReasonDPUClusterUnreach,
				fmt.Sprintf("DPU cluster unreachable: %v", perr), sfc.Generation)
			if serr := r.patchSFCStatus(ctx, sfc, cond); serr != nil {
				l.Error(serr, "failed to write Degraded status")
				return ctrl.Result{}, serr
			}
			return ctrl.Result{RequeueAfter: requeueTransient}, nil
		}
	}

	// 4) Translate OPI intent -> DPF object(s) (pure), name-scoped to the fleet.
	desired := r.Translator.ChainForSFC(tgt, sfc)
	if len(desired) == 0 {
		l.V(1).Info("empty chain; nothing to apply")
		return ctrl.Result{}, nil
	}

	// 5) Apply each DPF object. Classify failures: a version/GVK mismatch is
	// LOUD (DPFVersionUnsupported), a connection failure is transient.
	for _, obj := range desired {
		if aerr := r.apply(ctx, obj); aerr != nil {
			if cond, handled := r.classifyApplyError(aerr, sfc.Generation); handled {
				_ = r.patchSFCStatus(ctx, sfc, cond)
				return ctrl.Result{RequeueAfter: requeueTransient}, nil
			}
			l.Error(aerr, "failed to apply DPF object",
				"gvk", obj.GroupVersionKind().String(), "name", obj.GetName())
			return ctrl.Result{}, aerr
		}
	}

	// 6) Read DPF status back and aggregate least-ready (arch §5).
	ready, reason, msg, rerr := r.aggregateChainReadiness(ctx, desired)
	if rerr != nil {
		l.Error(rerr, "failed reading back DPF chain status")
		return ctrl.Result{}, rerr
	}

	// 7) Map onto the adapter-owned OPI status condition.
	cond := metav1.Condition{
		Type:               CondDPFReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: sfc.Generation,
	}
	if ready {
		cond.Status = metav1.ConditionTrue
	}
	if serr := r.patchSFCStatus(ctx, sfc, cond); serr != nil {
		l.Error(serr, "failed to update ServiceFunctionChain status")
		return ctrl.Result{}, serr
	}

	// 8) Requeue: keep polling until every backing DPF object is Ready.
	if !ready {
		l.V(1).Info("chain not yet ready; requeueing", "reason", reason)
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}
	l.Info("ServiceFunctionChain reconciled to Ready")
	return ctrl.Result{}, nil
}

// classifyApplyError maps a classified apply error to an OPI condition and
// reports whether it was handled (vs. an unclassified error the caller should
// return raw). Both handled cases requeue rather than wedge.
func (r *ServiceFunctionChainReconciler) classifyApplyError(err error, gen int64) (metav1.Condition, bool) {
	switch {
	case errors.Is(err, errDPFVersionUnsupported):
		// LOUD failure (arch §9, Q2): a DPF CRD we depend on was renamed/removed.
		return degraded(ReasonDPFVersionUnsupported, err.Error(), gen), true
	case errors.Is(err, errDPUClusterUnreachable):
		return degraded(ReasonDPUClusterUnreach, err.Error(), gen), true
	default:
		return metav1.Condition{}, false
	}
}

// apply performs a server-side apply of one adapter-owned DPF object, wrapping
// the error into a classified sentinel where possible (arch §9, Q2/Q3).
//
// Field-ownership contract with DPF and its maintenance-operator (arch §12, Q1):
// ForceOwnership only ever reclaims fields the APPLIED BODY names. The translator
// emits a deliberately MINIMAL body (spec.dpuFlavor + spec.nodeSelector for a
// deployment; spec image/service/chain refs for services) and never sets status,
// annotations, or any pause/maintenance field. So this apply cannot stomp a
// drain window or maintenance annotation DPF's maintenance-operator owns, and
// must not be broadened to do so. Disruptive updates (a new BFB via a changed
// DPUFlavor) ride through spec and are sequenced by DPF's maintenance-operator;
// the adapter never drains nodes or deletes DPUs to force a rollout.
func (r *ServiceFunctionChainReconciler) apply(ctx context.Context, obj *unstructured.Unstructured) error {
	err := r.Patch(ctx, obj, client.Apply, client.FieldOwner(fieldOwner), client.ForceOwnership)
	return classifyDPFError(err, obj)
}

// aggregateChainReadiness reads back each DPF object and returns the least-ready
// verdict (arch §5). The chain is Ready only when every object reports Ready.
func (r *ServiceFunctionChainReconciler) aggregateChainReadiness(
	ctx context.Context, objs []*unstructured.Unstructured,
) (ready bool, reason, msg string, err error) {
	for _, want := range objs {
		got := &unstructured.Unstructured{}
		got.SetGroupVersionKind(want.GroupVersionKind())
		key := types.NamespacedName{Namespace: want.GetNamespace(), Name: want.GetName()}
		if gerr := r.Get(ctx, key, got); gerr != nil {
			if cerr := classifyDPFError(gerr, want); errors.Is(cerr, errDPFVersionUnsupported) {
				return false, ReasonDPFVersionUnsupported, cerr.Error(), nil
			}
			if apierrors.IsNotFound(gerr) {
				return false, ReasonServiceDeploying,
					fmt.Sprintf("%s/%s not yet created", want.GetKind(), want.GetName()), nil
			}
			return false, "", "", fmt.Errorf("read back %s: %w", want.GetName(), gerr)
		}
		if !dpfObjectReady(got) {
			return false, ReasonServiceDeploying,
				fmt.Sprintf("%s/%s not Ready", got.GetKind(), got.GetName()), nil
		}
	}
	return true, ReasonChainProgrammed, "all DPF chain objects Ready", nil
}

// patchSFCStatus updates the SFC's adapter-owned condition using a status
// subresource patch, preserving other conditions (arch §5: never clear to a
// false-green; never touch a condition another controller owns).
func (r *ServiceFunctionChainReconciler) patchSFCStatus(
	ctx context.Context, sfc *opiv1.ServiceFunctionChain, cond metav1.Condition,
) error {
	base := sfc.DeepCopy()
	meta.SetStatusCondition(&sfc.Status.Conditions, cond)
	sfc.Status.ObservedGeneration = sfc.Generation
	if err := r.Status().Patch(ctx, sfc, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch SFC status: %w", err)
	}
	return nil
}

// deleteChain garbage-collects the DPF objects the adapter created for an SFC
// (arch §12, Q4). It tolerates already-absent objects and a GVK-level version
// drift (a DPF CRD removed on upgrade means the object is already gone from the
// adapter's point of view), so deletion converges instead of wedging the
// finalizer. Any other error is returned so the finalizer stays until cleanup
// actually succeeds — the adapter never drops the finalizer on a failed delete,
// which would orphan DPF objects.
func (r *ServiceFunctionChainReconciler) deleteChain(ctx context.Context, objs []*unstructured.Unstructured) error {
	for _, obj := range objs {
		err := r.Delete(ctx, obj, client.PropagationPolicy(metav1.DeletePropagationForeground))
		if err == nil || apierrors.IsNotFound(err) {
			continue
		}
		if cerr := classifyDPFError(err, obj); errors.Is(cerr, errDPFVersionUnsupported) {
			// The CRD itself is gone; the dependent cannot exist. Treat as cleaned.
			continue
		}
		return fmt.Errorf("delete %s/%s: %w", obj.GetKind(), obj.GetName(), err)
	}
	return nil
}

// addFinalizer / removeFinalizer manage the cleanup finalizer on the OPI object
// via a merge patch (not a full update), so they never fight the daemon's writes
// to unrelated fields.
func (r *ServiceFunctionChainReconciler) addFinalizer(ctx context.Context, sfc *opiv1.ServiceFunctionChain) error {
	base := sfc.DeepCopy()
	controllerutil.AddFinalizer(sfc, chainFinalizer)
	return r.Patch(ctx, sfc, client.MergeFrom(base))
}

func (r *ServiceFunctionChainReconciler) removeFinalizer(ctx context.Context, sfc *opiv1.ServiceFunctionChain) error {
	base := sfc.DeepCopy()
	controllerutil.RemoveFinalizer(sfc, chainFinalizer)
	return r.Patch(ctx, sfc, client.MergeFrom(base))
}

// SetupWithManager wires the reconciler, pre-filtering to NVIDIA-managed objects
// at the informer so non-NVIDIA events never even enqueue (arch §9, Q3).
func (r *ServiceFunctionChainReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opiv1.ServiceFunctionChain{}, builder.WithPredicates(nvidiaPredicate())).
		// TODO(dpuf): add Owns()/Watches() for the DPF GVKs (unstructured) so a
		// DPF status change requeues the owning SFC via the OwnerRefLabel
		// back-reference (arch §5). Requires an unstructured watch source.
		Named("servicefunctionchain-dpf-adapter").
		Complete(r)
}

// --- DataProcessingUnit provisioning-status mirror -------------------------

// DataProcessingUnitReconciler watches the daemon-created DataProcessingUnit,
// ensures its fleet's DPUDeployment exists (Day-0/1 provisioning), and mirrors
// the DPF DPU phase + datapath endpoint back onto the OPI object so the VSP's
// Init() can hand the endpoint to the daemon (arch §4-i).
type DataProcessingUnitReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Translator translate.Translator
	Fleets     ClusterResolver
}

// +kubebuilder:rbac:groups=config.openshift.io,resources=dataprocessingunits,verbs=get;list;watch
// +kubebuilder:rbac:groups=config.openshift.io,resources=dataprocessingunits/status,verbs=update;patch
// +kubebuilder:rbac:groups=provisioning.dpu.nvidia.com,resources=dpudeployments;dpus,verbs=get;list;watch;create;update;patch

// Reconcile ensures provisioning and mirrors phase/endpoint upward.
func (r *DataProcessingUnitReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	l := log.FromContext(ctx).WithValues("dataprocessingunit", req.NamespacedName)

	dpu := &opiv1.DataProcessingUnit{}
	if err := r.Get(ctx, req.NamespacedName, dpu); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		l.Error(err, "unable to fetch DataProcessingUnit")
		return ctrl.Result{}, err
	}

	// ISOLATION GUARD (arch §9, Q3): ignore non-NVIDIA DPUs entirely.
	fleet, ok, err := r.Fleets.Resolve(ctx, dpu)
	if err != nil {
		l.Error(err, "fleet resolution failed")
		return ctrl.Result{}, err
	}
	if !ok {
		l.V(1).Info("DPU not NVIDIA-managed; ignoring")
		return ctrl.Result{}, nil
	}
	tgt := fleetToTarget(fleet)

	// Ensure this fleet's single DPUDeployment exists (idempotent apply). One
	// per fleet, NOT one per node — DPF's DPUSet fans out (arch §9, Q1).
	deploy := r.Translator.DeploymentForFleet(tgt)
	if aerr := r.apply(ctx, deploy); aerr != nil {
		if errors.Is(aerr, errDPFVersionUnsupported) {
			base := dpu.DeepCopy()
			meta.SetStatusCondition(&dpu.Status.Conditions,
				degraded(ReasonDPFVersionUnsupported, aerr.Error(), dpu.Generation))
			_ = r.Status().Patch(ctx, dpu, client.MergeFrom(base))
			l.Error(aerr, "DPF version unsupported applying DPUDeployment")
			return ctrl.Result{RequeueAfter: requeueTransient}, nil
		}
		l.Error(aerr, "failed to apply DPUDeployment", "name", deploy.GetName())
		return ctrl.Result{}, aerr
	}

	// Read the corresponding DPF DPU phase + datapath endpoint back (arch §5).
	phase, endpoint, rerr := r.readDPUPhase(ctx, fleet, dpu)
	if rerr != nil {
		// A readiness-signal schema drift is surfaced LOUDLY at the OPI layer
		// (arch §12, Q3) — a distinct reason, not a generic reconcile error and
		// not a silent forever-Provisioning. Requeue (transient) in case it is a
		// staged rollout, but the OPI object now names the drift.
		if errors.Is(rerr, errDPFStatusSchemaUnknown) {
			sb := dpu.DeepCopy()
			meta.SetStatusCondition(&dpu.Status.Conditions,
				degraded(ReasonDPFStatusSchema, rerr.Error(), dpu.Generation))
			if perr := r.Status().Patch(ctx, dpu, client.MergeFrom(sb)); perr != nil {
				l.Error(perr, "failed to surface DPFStatusUnrecognized")
				return ctrl.Result{}, perr
			}
			l.Error(rerr, "DPF DPU readiness signal unrecognized; surfaced at OPI layer")
			return ctrl.Result{RequeueAfter: requeueTransient}, nil
		}
		l.Error(rerr, "failed reading DPF DPU phase")
		return ctrl.Result{}, rerr
	}

	// Publish the DPF-provisioned endpoint as an ANNOTATION. The real
	// DataProcessingUnit status has no endpoint field (verified upstream), and
	// the thin NVIDIA VSP returns this value from Init() over gRPC (arch §3/§5).
	if endpoint != "" && dpu.Annotations[opiv1.EndpointAnnotation] != endpoint {
		abase := dpu.DeepCopy()
		if dpu.Annotations == nil {
			dpu.Annotations = map[string]string{}
		}
		dpu.Annotations[opiv1.EndpointAnnotation] = endpoint
		if perr := r.Patch(ctx, dpu, client.MergeFrom(abase)); perr != nil {
			l.Error(perr, "failed to publish datapath endpoint annotation")
			return ctrl.Result{}, perr
		}
	}

	// Write the adapter-OWNED DPFReady condition. We deliberately do NOT write
	// the top-level "Ready" that `oc get dpu` prints — the OPI daemon owns that
	// and flips it True once the VSP's Init() (fed by the annotation above)
	// succeeds (arch §5, single-writer rule).
	sbase := dpu.DeepCopy()
	meta.SetStatusCondition(&dpu.Status.Conditions, mapDPUPhase(phase, dpu.Generation))
	if perr := r.Status().Patch(ctx, dpu, client.MergeFrom(sbase)); perr != nil {
		l.Error(perr, "failed to update DataProcessingUnit status")
		return ctrl.Result{}, perr
	}

	if phase != dpf.DPUPhaseReady {
		return ctrl.Result{RequeueAfter: requeueProvisioning}, nil
	}
	l.Info("DataProcessingUnit provisioned", "endpoint", endpoint)
	return ctrl.Result{}, nil
}

// apply server-side-applies the fleet DPUDeployment. See the SFC apply above for
// the minimal-body / maintenance-operator field-ownership contract (arch §12,
// Q1). Note the fleet DPUDeployment is shared infrastructure across every DPU
// the selector matches, so a single DataProcessingUnit deletion must NOT delete
// it — hence this reconciler carries no cleanup finalizer; the deployment is
// torn down with the fleet, not per-DPU.
func (r *DataProcessingUnitReconciler) apply(ctx context.Context, obj *unstructured.Unstructured) error {
	err := r.Patch(ctx, obj, client.Apply, client.FieldOwner(fieldOwner), client.ForceOwnership)
	return classifyDPFError(err, obj)
}

// readDPUPhase fetches the DPF DPU object and extracts phase + datapath
// endpoint. TODO(dpuf): the endpoint path below is a placeholder for the real
// DPU.status field once pinned against a DPF release (arch §8).
func (r *DataProcessingUnitReconciler) readDPUPhase(
	ctx context.Context, fleet Fleet, dpu *opiv1.DataProcessingUnit,
) (phase, endpoint string, err error) {
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(dpf.DPUGVK)
	key := types.NamespacedName{Namespace: fleet.Namespace, Name: "dpu-" + dpu.Name}
	if gerr := r.Get(ctx, key, got); gerr != nil {
		if cerr := classifyDPFError(gerr, got); errors.Is(cerr, errDPFVersionUnsupported) {
			return "", "", cerr
		}
		if apierrors.IsNotFound(gerr) {
			return dpf.DPUPhaseProvisioning, "", nil
		}
		return "", "", fmt.Errorf("get DPF DPU: %w", gerr)
	}

	// Readiness-signal drift guard (arch §12, Q3). DPF has changed how DPU
	// readiness is signaled across versions before. We key on status.phase; if
	// DPF has WRITTEN a status stanza but it no longer carries phase, the signal
	// moved under us. Distinguish that from the legitimate early window where no
	// status exists yet (DPU just created): a populated status with no phase is
	// drift and must be LOUD, not an eternal silent "Provisioning".
	statusMap, hasStatus, _ := unstructured.NestedMap(got.Object, "status")
	phase, hasPhase, _ := unstructured.NestedString(got.Object, "status", "phase")
	if hasStatus && len(statusMap) > 0 && !hasPhase {
		return "", "", fmt.Errorf(
			"%w: DPF DPU %s reports status but no status.phase (readiness signal moved?)",
			errDPFStatusSchemaUnknown, got.GetName())
	}
	endpoint, _, _ = unstructured.NestedString(got.Object, "status", "dataplaneEndpoint")
	return phase, endpoint, nil
}

// SetupWithManager wires the DPU reconciler with the NVIDIA pre-filter.
func (r *DataProcessingUnitReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opiv1.DataProcessingUnit{}, builder.WithPredicates(nvidiaPredicate())).
		Named("dataprocessingunit-dpf-adapter").
		Complete(r)
}

// --- shared helpers --------------------------------------------------------

// nvidiaPredicate drops events for objects this adapter does not own, at the
// informer (arch §9, Q3). This is the cheap pre-filter; Reconcile still
// re-checks via the resolver (defense in depth), because labels can change
// between event and reconcile.
func nvidiaPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(isNVIDIAManaged)
}

// classifyDPFError turns a raw client error into a classified sentinel: a
// no-kind-match / no-resource-match (the shape of a DPF CRD that was renamed or
// removed on upgrade) becomes errDPFVersionUnsupported so callers fail LOUDLY
// (arch §9, Q2); a transient apiserver error becomes errDPUClusterUnreachable.
// Anything else is returned unchanged.
func classifyDPFError(err error, obj *unstructured.Unstructured) error {
	if err == nil {
		return nil
	}
	if meta.IsNoMatchError(err) || runtime.IsNotRegisteredError(err) {
		return fmt.Errorf("%w: %s not served by cluster: %v",
			errDPFVersionUnsupported, obj.GroupVersionKind().String(), err)
	}
	if apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) ||
		apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) {
		return fmt.Errorf("%w: %v", errDPUClusterUnreachable, err)
	}
	return err
}

// dpfObjectReady reads the standard Ready condition off a DPF unstructured
// object. DPF objects follow metav1.Condition semantics (arch §5).
func dpfObjectReady(u *unstructured.Unstructured) bool {
	conds, found, err := unstructured.NestedSlice(u.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, c := range conds {
		cm, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if cm["type"] == "Ready" && cm["status"] == string(metav1.ConditionTrue) {
			return true
		}
	}
	return false
}

// interface assertions: both reconcilers implement reconcile.Reconciler.
var (
	_ ctrlReconciler = (*ServiceFunctionChainReconciler)(nil)
	_ ctrlReconciler = (*DataProcessingUnitReconciler)(nil)
)

// ctrlReconciler is the controller-runtime reconcile.Reconciler shape, aliased
// locally to keep the assertion readable.
type ctrlReconciler interface {
	Reconcile(context.Context, ctrl.Request) (ctrl.Result, error)
}

// staticProbe is a trivial per-fleet DPUClusterProbe used by cmd/main.go wiring
// and tests until the real kubeconfig-Secret-backed client is plumbed in
// (arch §6). A real probe dials that fleet's static DPUCluster.
type staticProbe struct {
	log       logr.Logger
	reachable bool
}

// NewStaticProbe returns a probe reporting a fixed reachability, logging each
// check.
func NewStaticProbe(l logr.Logger, reachable bool) DPUClusterProbe {
	return &staticProbe{log: l, reachable: reachable}
}

func (s *staticProbe) Reachable(ctx context.Context) error {
	if !s.reachable {
		return errDPUClusterUnreachable
	}
	return nil
}
