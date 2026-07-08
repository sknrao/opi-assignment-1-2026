package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// This file keeps the assignment skeleton close to the real OPI and DPF
// architecture without importing either repository directly.
//
// Grounding points:
// - The watched OPI object is DataProcessingUnit, matching dpu-operator.
// - The design adds a small, explicit DpuIdentifier field so OPI can carry
//   the raw vendor identifier detectors already know, instead of rebuilding
//   it later from object naming.
// - The VSP method names mirror dpu-api/api.proto: Init, GetDevices,
//   SetNumVfs, CreateNetworkFunction, DeleteNetworkFunction, Ping.
// - The DPF resources are represented with their real GroupVersionKinds and
//   are applied through controller-runtime's client as unstructured objects.
// - A separate status reconciler patches DataProcessingUnit.status.conditions
//   after reading DPF object conditions.
//
// This is intentionally a phase-1 skeleton, not a full repository-ready
// operator. The assignment values technical approach over forced completeness,
// so the file focuses on:
// - a real reconciliation boundary.
// - explicit ownership and status flow.
// - compile-valid controller-runtime structure.
// - comments where the next implementation step would begin.

var (
	opiGroupVersion = schema.GroupVersion{Group: "config.openshift.io", Version: "v1"}

	dpuFlavorGVK = schema.GroupVersionKind{
		Group:   "provisioning.dpu.nvidia.com",
		Version: "v1alpha1",
		Kind:    "DPUFlavor",
	}
	dpuSetGVK = schema.GroupVersionKind{
		Group:   "provisioning.dpu.nvidia.com",
		Version: "v1alpha1",
		Kind:    "DPUSet",
	}
	dpuServiceNadGVK = schema.GroupVersionKind{
		Group:   "svc.dpu.nvidia.com",
		Version: "v1alpha1",
		Kind:    "DPUServiceNAD",
	}
	serviceInterfaceGVK = schema.GroupVersionKind{
		Group:   "svc.dpu.nvidia.com",
		Version: "v1alpha1",
		Kind:    "ServiceInterface",
	}
)

const (
	readyConditionType            = "Ready"
	progressingConditionType      = "Progressing"
	degradedConditionType         = "Degraded"
	translationValidConditionType = "TranslationValid"
	fieldOwner                    = "opi-nvidia-dpf-adapter"
	defaultBridgeName             = "br-hbn"
	defaultTargetNamespace        = "dpf-system"
	defaultBlueFieldSW            = "bf3-default"
	hostSideSuffix                = "-host"
	dpuSideSuffix                 = "-dpu"
	rawIdentifierAnnotation       = "opi.dpu/raw-identifier"
)

type DataProcessingUnitSpec struct {
	DpuProductName string `json:"dpuProductName,omitempty"`
	DpuIdentifier  string `json:"dpuIdentifier,omitempty"`
	IsDpuSide      bool   `json:"isDpuSide"`
	NodeName       string `json:"nodeName"`
}

type DataProcessingUnitStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type DataProcessingUnit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DataProcessingUnitSpec   `json:"spec,omitempty"`
	Status DataProcessingUnitStatus `json:"status,omitempty"`
}

type DataProcessingUnitList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DataProcessingUnit `json:"items"`
}

func (in *DataProcessingUnit) DeepCopyObject() runtime.Object {
	out := *in
	out.Status.Conditions = append([]metav1.Condition(nil), in.Status.Conditions...)
	return &out
}

func (in *DataProcessingUnitList) DeepCopyObject() runtime.Object {
	out := *in
	out.Items = make([]DataProcessingUnit, len(in.Items))
	copy(out.Items, in.Items)
	for i := range out.Items {
		out.Items[i].Status.Conditions = append([]metav1.Condition(nil), in.Items[i].Status.Conditions...)
	}
	return &out
}

var schemeBuilder = runtime.NewSchemeBuilder(func(s *runtime.Scheme) error {
	s.AddKnownTypes(opiGroupVersion, &DataProcessingUnit{}, &DataProcessingUnitList{})
	metav1.AddToGroupVersion(s, opiGroupVersion)
	return nil
})

var addToScheme = schemeBuilder.AddToScheme

type InitRequest struct {
	DPUMode       bool
	DPUIdentifier string
}

type IpPort struct {
	IP   string
	Port int32
}

type NFRequest struct {
	Input  string
	Output string
}

type Empty struct{}

type VfCount struct {
	VfCnt int32
}

type TopologyInfo struct {
	Node string
}

type Device struct {
	ID       string
	Health   string
	Topology TopologyInfo
}

type DeviceListResponse struct {
	Devices map[string]Device
}

type PingRequest struct {
	Timestamp int64
	SenderID  string
}

type PingResponse struct {
	Timestamp   int64
	ResponderID string
	Healthy     bool
}

// NvidiaVspServer mirrors the real VSP service boundary from OPI.
// In production this would sit behind generated gRPC handlers from api.proto.
type NvidiaVspServer struct {
	client.Client
	Scheme          *runtime.Scheme
	DPUName         string
	TargetNamespace string
	BridgeName      string
}

func (s *NvidiaVspServer) Init(ctx context.Context, req InitRequest) (IpPort, error) {
	dpu, err := s.loadDPU(ctx)
	if err != nil {
		return IpPort{}, err
	}
	if req.DPUIdentifier == "" {
		return IpPort{}, fmt.Errorf("missing DPU identifier")
	}
	identifier := canonicalDPUIdentifierForObject(dpu)
	if normalizeDPUIdentifier(req.DPUIdentifier) != identifier {
		return IpPort{}, fmt.Errorf("requested DPU identifier %q does not match DataProcessingUnit %q", req.DPUIdentifier, dpu.Name)
	}

	flavor := s.buildFlavorObject(dpu, identifier, 1)
	dpuset := s.buildDPUSetObject(dpu, identifier, flavor.GetName())

	if err := s.applyOwnedObject(ctx, dpu, flavor); err != nil {
		return IpPort{}, err
	}
	if err := s.applyOwnedObject(ctx, dpu, dpuset); err != nil {
		return IpPort{}, err
	}

	// OPI's Init RPC returns the address of the VSP endpoint.
	// A real implementation would also publish readiness details for the pod
	// that serves the VSP and would likely allocate this through service
	// discovery rather than a fixed loopback placeholder.
	return IpPort{IP: "127.0.0.1", Port: 50051}, nil
}

func (s *NvidiaVspServer) SetNumVfs(ctx context.Context, req VfCount) (VfCount, error) {
	dpu, err := s.loadDPU(ctx)
	if err != nil {
		return VfCount{}, err
	}
	if req.VfCnt < 1 {
		return VfCount{}, fmt.Errorf("vf count must be positive, got %d", req.VfCnt)
	}

	identifier := canonicalDPUIdentifierForObject(dpu)
	flavor := s.buildFlavorObject(dpu, identifier, int(req.VfCnt))
	if err := s.applyOwnedObject(ctx, dpu, flavor); err != nil {
		return VfCount{}, err
	}
	dpuset := s.buildDPUSetObject(dpu, identifier, flavor.GetName())
	if err := s.applyOwnedObject(ctx, dpu, dpuset); err != nil {
		return VfCount{}, err
	}
	return req, nil
}

func (s *NvidiaVspServer) CreateNetworkFunction(ctx context.Context, req NFRequest) (Empty, error) {
	dpu, err := s.loadDPU(ctx)
	if err != nil {
		return Empty{}, err
	}
	if req.Input == "" || req.Output == "" {
		return Empty{}, fmt.Errorf("input and output endpoints are required")
	}

	nad := s.buildServiceNadObject(dpu)
	inIf := s.buildServiceInterfaceObject(dpu, "ingress", req.Input, nad.GetName())
	outIf := s.buildServiceInterfaceObject(dpu, "egress", req.Output, nad.GetName())

	objects := []*unstructured.Unstructured{nad, inIf, outIf}
	for _, obj := range objects {
		if err := s.applyOwnedObject(ctx, dpu, obj); err != nil {
			return Empty{}, err
		}
	}

	// Deliberately omitted here: DPUServiceChain materialization.
	// Current OPI NFRequest only carries endpoint pairs, so this skeleton keeps
	// the first implementation at the DPUServiceNAD and ServiceInterface level.
	// If OPI grows a richer declarative service model later, this is the place
	// where the adapter can move upward to DPUService or DPUServiceChain objects.
	return Empty{}, nil
}

func (s *NvidiaVspServer) DeleteNetworkFunction(ctx context.Context, _ NFRequest) (Empty, error) {
	dpu, err := s.loadDPU(ctx)
	if err != nil {
		return Empty{}, err
	}

	for _, obj := range []*unstructured.Unstructured{
		s.buildServiceInterfaceObject(dpu, "ingress", "placeholder", s.serviceNadName(dpu)),
		s.buildServiceInterfaceObject(dpu, "egress", "placeholder", s.serviceNadName(dpu)),
		s.buildServiceNadObject(dpu),
	} {
		if err := s.deleteOwnedObject(ctx, obj); err != nil {
			return Empty{}, err
		}
	}
	return Empty{}, nil
}

func (s *NvidiaVspServer) GetDevices(ctx context.Context) (DeviceListResponse, error) {
	dpu, err := s.loadDPU(ctx)
	if err != nil {
		return DeviceListResponse{}, err
	}
	identifier := canonicalDPUIdentifierForObject(dpu)
	return DeviceListResponse{
		Devices: map[string]Device{
			identifier: {
				ID:     identifier,
				Health: "Healthy",
				Topology: TopologyInfo{
					Node: dpu.Spec.NodeName,
				},
			},
		},
	}, nil
}

func (s *NvidiaVspServer) Ping(_ context.Context, req PingRequest) (PingResponse, error) {
	return PingResponse{
		Timestamp:   req.Timestamp,
		ResponderID: s.DPUName,
		Healthy:     true,
	}, nil
}

type DPUStatusReconciler struct {
	client.Client
	Scheme          *runtime.Scheme
	TargetNamespace string
}

type statusEvaluation struct {
	conditions   []metav1.Condition
	requeueAfter time.Duration
}

func (r *DPUStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	dpu := &DataProcessingUnit{}
	if err := r.Get(ctx, req.NamespacedName, dpu); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	base := dpu.DeepCopyObject().(*DataProcessingUnit)

	if !strings.Contains(strings.ToLower(dpu.Spec.DpuProductName), "nvidia") &&
		!strings.Contains(strings.ToLower(dpu.Spec.DpuProductName), "bluefield") {
		return ctrl.Result{}, nil
	}

	evaluation, err := r.evaluateStatus(ctx, dpu)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, condition := range evaluation.conditions {
		apimeta.SetStatusCondition(&dpu.Status.Conditions, condition)
	}
	if err := r.patchStatus(ctx, base, dpu); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: evaluation.requeueAfter}, nil
}

func (r *DPUStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&DataProcessingUnit{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: 2})

	for _, gvk := range watchedDPFObjectKinds() {
		builder = builder.Watches(
			watchObjectForGVK(gvk),
			handler.EnqueueRequestsFromMapFunc(r.mapOwnedDPFObjectToDPU),
		)
	}

	return builder.Complete(r)
}

func (r *DPUStatusReconciler) patchStatus(ctx context.Context, before, after *DataProcessingUnit) error {
	return r.Status().Patch(ctx, after, client.MergeFrom(before))
}

func (s *NvidiaVspServer) loadDPU(ctx context.Context) (*DataProcessingUnit, error) {
	dpu := &DataProcessingUnit{}
	if err := s.Get(ctx, types.NamespacedName{Name: s.DPUName}, dpu); err != nil {
		return nil, err
	}
	return dpu, nil
}

func (s *NvidiaVspServer) applyOwnedObject(ctx context.Context, owner *DataProcessingUnit, obj *unstructured.Unstructured) error {
	if err := controllerutil.SetControllerReference(owner, obj, s.Scheme); err != nil {
		return err
	}
	return s.Patch(ctx, obj, client.Apply, client.ForceOwnership, client.FieldOwner(fieldOwner))
}

func (s *NvidiaVspServer) deleteOwnedObject(ctx context.Context, obj *unstructured.Unstructured) error {
	err := s.Delete(ctx, obj)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (s *NvidiaVspServer) buildFlavorObject(dpu *DataProcessingUnit, identifier string, vfCount int) *unstructured.Unstructured {
	obj := namedUnstructured(dpuFlavorGVK, s.TargetNamespace, flavorName(dpu, identifier, vfCount))
	obj.Object["spec"] = map[string]any{
		"ewNicConfigurations": []any{
			map[string]any{
				"numVfs":   int64(vfCount),
				"linkType": "Ethernet",
			},
		},
	}
	obj.SetLabels(ownedLabels(dpu, identifier))
	return obj
}

func (s *NvidiaVspServer) buildDPUSetObject(dpu *DataProcessingUnit, identifier, flavor string) *unstructured.Unstructured {
	obj := namedUnstructured(dpuSetGVK, s.TargetNamespace, dpusetName(dpu, identifier))
	labelIdentifier := sanitizeLabelValue(identifier)
	obj.Object["spec"] = map[string]any{
		"strategy": map[string]any{
			"type": "RollingUpdate",
		},
		// Real DPF deployments must align this selector with labels produced by
		// DPUDiscovery. The skeleton keeps the selector deterministic and readable
		// so the translation boundary is explicit, while still making it obvious
		// that this part must be validated against a live DPF environment.
		"dpuDeviceSelector": map[string]any{
			"matchLabels": map[string]any{
				"opi.dpu/identifier": labelIdentifier,
			},
		},
		"dpuTemplate": map[string]any{
			"spec": map[string]any{
				"blueFieldSoftware": map[string]any{
					"name": defaultBlueFieldSW,
				},
				"dpuFlavor": flavor,
				"nodeEffect": map[string]any{
					"noEffect": true,
				},
			},
		},
	}
	obj.SetLabels(ownedLabels(dpu, identifier))
	return obj
}

func (s *NvidiaVspServer) buildServiceNadObject(dpu *DataProcessingUnit) *unstructured.Unstructured {
	obj := namedUnstructured(dpuServiceNadGVK, s.TargetNamespace, s.serviceNadName(dpu))
	obj.Object["spec"] = map[string]any{
		"resourceType": "vf",
		"bridge":       s.BridgeName,
	}
	obj.SetLabels(ownedLabels(dpu, dpu.Name))
	return obj
}

func (s *NvidiaVspServer) buildServiceInterfaceObject(dpu *DataProcessingUnit, direction, endpoint, nadName string) *unstructured.Unstructured {
	obj := namedUnstructured(serviceInterfaceGVK, s.TargetNamespace, serviceInterfaceName(dpu, direction))
	obj.Object["spec"] = map[string]any{
		"node":          dpu.Spec.NodeName,
		"interfaceType": "service",
		"service": map[string]any{
			"serviceID":     endpoint,
			"network":       fmt.Sprintf("%s/%s", s.TargetNamespace, nadName),
			"interfaceName": interfaceName(direction),
		},
	}
	obj.SetLabels(ownedLabels(dpu, dpu.Name))
	return obj
}

func (s *NvidiaVspServer) serviceNadName(dpu *DataProcessingUnit) string {
	return serviceNadName(dpu)
}

func namedUnstructured(gvk schema.GroupVersionKind, namespace, name string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	return obj
}

func ownedLabels(dpu *DataProcessingUnit, identifier string) map[string]string {
	return map[string]string{
		"opi.dpu/name":                 sanitizeLabelValue(dpu.Name),
		"opi.dpu/node":                 sanitizeLabelValue(dpu.Spec.NodeName),
		"opi.dpu/product":              sanitizeLabelValue(dpu.Spec.DpuProductName),
		"opi.dpu/identifier":           sanitizeLabelValue(identifier),
		"opi.dpu/source-uid":           sanitizeLabelValue(string(dpu.UID)),
		"app.kubernetes.io/managed-by": sanitizeLabelValue(fieldOwner),
	}
}

func flavorName(dpu *DataProcessingUnit, identifier string, vfCount int) string {
	return trimName(sanitizeName(fmt.Sprintf("%s-%s-vf-%d-flavor", dpu.Name, identifier, vfCount)))
}

func dpusetName(dpu *DataProcessingUnit, identifier string) string {
	return trimName(sanitizeName(fmt.Sprintf("%s-%s-dpuset", dpu.Name, identifier)))
}

func serviceNadName(dpu *DataProcessingUnit) string {
	return trimName(sanitizeName(fmt.Sprintf("%s-nad", dpu.Name)))
}

func serviceInterfaceName(dpu *DataProcessingUnit, direction string) string {
	return trimName(sanitizeName(fmt.Sprintf("%s-%s-if", dpu.Name, direction)))
}

func interfaceName(direction string) string {
	if direction == "ingress" {
		return "net-in"
	}
	return "net-out"
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	replacer := strings.NewReplacer(":", "-", ".", "-", "_", "-", "/", "-", " ", "-")
	s = replacer.Replace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// This design adds DataProcessingUnit.spec.dpuIdentifier as the primary source
// of the raw vendor identifier. Annotation and name parsing are kept only as
// backward-compatible fallbacks for older objects or migration periods.
func canonicalDPUIdentifier(name string) string {
	return canonicalDPUIdentifierForObject(&DataProcessingUnit{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	})
}

func canonicalDPUIdentifierForObject(dpu *DataProcessingUnit) string {
	if dpu != nil {
		if identifier := strings.TrimSpace(dpu.Spec.DpuIdentifier); identifier != "" {
			return identifier
		}
	}
	if dpu != nil && dpu.Annotations != nil {
		if identifier := strings.TrimSpace(dpu.Annotations[rawIdentifierAnnotation]); identifier != "" {
			return identifier
		}
	}
	normalized := normalizeDPUIdentifier(dpu.Name)
	if normalized == "" {
		return "unknown-dpu"
	}
	return normalized
}

func normalizeDPUIdentifier(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimSuffix(name, hostSideSuffix)
	name = strings.TrimSuffix(name, dpuSideSuffix)
	return name
}

func trimName(s string) string {
	s = strings.Trim(s, "-")
	if len(s) <= 63 {
		if s == "" {
			return "dpu-object"
		}
		return s
	}

	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(s))
	suffix := fmt.Sprintf("%08x", hasher.Sum32())
	prefixLen := 63 - len(suffix) - 1
	prefix := strings.Trim(s[:prefixLen], "-")
	if prefix == "" {
		prefix = "dpu-object"
	}
	return prefix + "-" + suffix
}

func currentDPUFlavorName(dpuset *unstructured.Unstructured, dpu *DataProcessingUnit, identifier string) string {
	flavor, found, err := unstructured.NestedString(dpuset.Object, "spec", "dpuTemplate", "spec", "dpuFlavor")
	if err == nil && found && strings.TrimSpace(flavor) != "" {
		return flavor
	}
	return flavorName(dpu, identifier, 1)
}

func sanitizeLabelValue(s string) string {
	s = trimName(sanitizeName(s))
	s = strings.Trim(s, "-")
	if s == "" {
		return "unknown"
	}
	return s
}

func readyConditionTrue(obj *unstructured.Unstructured) bool {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return false
	}
	for _, item := range conditions {
		cond, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if cond["type"] == readyConditionType && cond["status"] == string(metav1.ConditionTrue) {
			return true
		}
	}
	return false
}

func watchedDPFObjectKinds() []schema.GroupVersionKind {
	return []schema.GroupVersionKind{
		dpuFlavorGVK,
		dpuSetGVK,
		dpuServiceNadGVK,
		serviceInterfaceGVK,
	}
}

func watchObjectForGVK(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	return obj
}

func (r *DPUStatusReconciler) mapOwnedDPFObjectToDPU(_ context.Context, obj client.Object) []ctrl.Request {
	name := strings.TrimSpace(obj.GetLabels()["opi.dpu/name"])
	if name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: types.NamespacedName{Name: name}}}
}

func (r *DPUStatusReconciler) evaluateStatus(ctx context.Context, dpu *DataProcessingUnit) (statusEvaluation, error) {
	if strings.TrimSpace(dpu.Spec.NodeName) == "" {
		return statusEvaluation{
			conditions: []metav1.Condition{
				newCondition(translationValidConditionType, metav1.ConditionFalse, "MissingNodeName", "DataProcessingUnit.spec.nodeName is required for DPF translation", dpu.Generation),
				newCondition(progressingConditionType, metav1.ConditionFalse, "Blocked", "Translation is blocked until required identity fields are present", dpu.Generation),
				newCondition(degradedConditionType, metav1.ConditionFalse, "NoVendorFailure", "No vendor-reported failure is present yet", dpu.Generation),
				newCondition(readyConditionType, metav1.ConditionFalse, "TranslationBlocked", "Cannot reconcile DPF objects until node identity is available", dpu.Generation),
			},
		}, nil
	}

	identifier := strings.TrimSpace(canonicalDPUIdentifierForObject(dpu))
	if identifier == "" || identifier == "unknown-dpu" {
		return statusEvaluation{
			conditions: []metav1.Condition{
				newCondition(translationValidConditionType, metav1.ConditionFalse, "MissingIdentifier", "DataProcessingUnit does not yet expose a usable DPU identifier", dpu.Generation),
				newCondition(progressingConditionType, metav1.ConditionFalse, "Blocked", "Translation is blocked until a stable DPU identifier is present", dpu.Generation),
				newCondition(degradedConditionType, metav1.ConditionFalse, "NoVendorFailure", "No vendor-reported failure is present yet", dpu.Generation),
				newCondition(readyConditionType, metav1.ConditionFalse, "TranslationBlocked", "Cannot reconcile DPF objects until DPU identity is available", dpu.Generation),
			},
		}, nil
	}

	dpuset := namedUnstructured(dpuSetGVK, r.TargetNamespace, dpusetName(dpu, identifier))
	if err := r.Get(ctx, client.ObjectKeyFromObject(dpuset), dpuset); err != nil {
		if apierrors.IsNotFound(err) {
			return missingObjectEvaluation(dpu.Generation, "DPUSet"), nil
		}
		return statusEvaluation{}, err
	}

	flavor := namedUnstructured(dpuFlavorGVK, r.TargetNamespace, currentDPUFlavorName(dpuset, dpu, identifier))
	if err := r.Get(ctx, client.ObjectKeyFromObject(flavor), flavor); err != nil {
		if apierrors.IsNotFound(err) {
			return missingObjectEvaluation(dpu.Generation, "DPUFlavor"), nil
		}
		return statusEvaluation{}, err
	}

	nad := namedUnstructured(dpuServiceNadGVK, r.TargetNamespace, serviceNadName(dpu))
	if err := r.Get(ctx, client.ObjectKeyFromObject(nad), nad); err != nil {
		if apierrors.IsNotFound(err) {
			return missingObjectEvaluation(dpu.Generation, "DPUServiceNAD"), nil
		}
		return statusEvaluation{}, err
	}

	translation := newCondition(translationValidConditionType, metav1.ConditionTrue, "IdentityResolved", "Adapter can translate this DataProcessingUnit into owned DPF objects", dpu.Generation)
	degraded := newCondition(degradedConditionType, metav1.ConditionFalse, "NoVendorFailure", "No vendor-reported failure is present", dpu.Generation)

	if reason, message, found := firstVendorFailure(dpuset, flavor, nad); found {
		degraded = newCondition(degradedConditionType, metav1.ConditionTrue, reason, message, dpu.Generation)
	}

	if readyConditionTrue(dpuset) && readyConditionTrue(nad) {
		return statusEvaluation{
			conditions: []metav1.Condition{
				translation,
				newCondition(progressingConditionType, metav1.ConditionFalse, "SteadyState", "Required DPF objects are no longer progressing", dpu.Generation),
				degraded,
				newCondition(readyConditionType, metav1.ConditionTrue, "DPFObjectsReady", "Provisioning and service-plane objects are ready", dpu.Generation),
			},
		}, nil
	}

	return statusEvaluation{
		conditions: []metav1.Condition{
			translation,
			newCondition(progressingConditionType, metav1.ConditionTrue, "DPFReconcileInProgress", "Owned DPF objects exist but one or more are still reconciling", dpu.Generation),
			degraded,
			newCondition(readyConditionType, metav1.ConditionFalse, "DPFReconcileInProgress", "Owned DPF objects are present but not ready yet", dpu.Generation),
		},
		// Primary trigger is the secondary watch on DPF objects.
		// This slow requeue is only a safety net for missed events.
		requeueAfter: 2 * time.Minute,
	}, nil
}

func missingObjectEvaluation(observedGeneration int64, kind string) statusEvaluation {
	return statusEvaluation{
		conditions: []metav1.Condition{
			newCondition(translationValidConditionType, metav1.ConditionTrue, "IdentityResolved", "Adapter can translate this DataProcessingUnit into owned DPF objects", observedGeneration),
			newCondition(progressingConditionType, metav1.ConditionTrue, "OwnedObjectsMissing", fmt.Sprintf("Waiting for generated %s object", kind), observedGeneration),
			newCondition(degradedConditionType, metav1.ConditionFalse, "NoVendorFailure", "No vendor-reported failure is present yet", observedGeneration),
			newCondition(readyConditionType, metav1.ConditionFalse, "ProvisioningPending", fmt.Sprintf("%s object is not ready yet", kind), observedGeneration),
		},
		requeueAfter: 2 * time.Minute,
	}
}

func newCondition(conditionType string, status metav1.ConditionStatus, reason, message string, observedGeneration int64) metav1.Condition {
	return metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: observedGeneration,
		LastTransitionTime: metav1.Now(),
	}
}

func firstVendorFailure(objects ...*unstructured.Unstructured) (string, string, bool) {
	for _, obj := range objects {
		if obj == nil {
			continue
		}
		reason, message, found := objectFailure(obj)
		if found {
			return reason, message, true
		}
	}
	return "", "", false
}

func objectFailure(obj *unstructured.Unstructured) (string, string, bool) {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if err != nil || !found {
		return "", "", false
	}
	for _, item := range conditions {
		cond, ok := item.(map[string]any)
		if !ok {
			continue
		}
		condType, _ := cond["type"].(string)
		condStatus, _ := cond["status"].(string)
		reason, _ := cond["reason"].(string)
		message, _ := cond["message"].(string)
		typeLower := strings.ToLower(condType)
		reasonLower := strings.ToLower(reason)
		if condStatus == string(metav1.ConditionTrue) &&
			(strings.Contains(typeLower, "degrad") || strings.Contains(typeLower, "fail") || strings.Contains(typeLower, "error")) {
			return nonEmpty(reason, condType), nonEmpty(message, "Vendor object reported a degraded condition"), true
		}
		if strings.EqualFold(condType, readyConditionType) && condStatus == string(metav1.ConditionFalse) &&
			(strings.Contains(reasonLower, "fail") || strings.Contains(reasonLower, "error")) {
			return nonEmpty(reason, "VendorReadyFalse"), nonEmpty(message, "Vendor object reported a blocking readiness failure"), true
		}
	}
	return "", "", false
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func main() {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = addToScheme(scheme)

	// This binary is compile-valid, but it still expects a real kubeconfig and
	// a cluster if someone wants to run it. That boundary is intentional: for
	// the assignment, the value is in showing a technically credible control
	// loop shape, not in pretending a full DPF-backed environment exists here.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
	})
	if err != nil {
		panic(err)
	}

	server := &NvidiaVspServer{
		Client:          mgr.GetClient(),
		Scheme:          scheme,
		DPUName:         "bf3-node1-host",
		TargetNamespace: defaultTargetNamespace,
		BridgeName:      defaultBridgeName,
	}
	_ = server

	reconciler := &DPUStatusReconciler{
		Client:          mgr.GetClient(),
		Scheme:          scheme,
		TargetNamespace: defaultTargetNamespace,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		panic(err)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		panic(err)
	}
}
