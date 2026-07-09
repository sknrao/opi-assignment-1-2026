package controllers

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	opiv1alpha1 "opi-nvidia-adapter/api/v1alpha1"
)

func TestDPUClusterReconciler_Reconcile(t *testing.T) {
	// Set up scheme
	scheme := runtime.NewScheme()
	err := opiv1alpha1.AddToScheme(scheme)
	if err != nil {
		t.Fatalf("failed to add scheme: %v", err)
	}

	// Create test OPI DPUCluster CR
	cluster := &opiv1alpha1.DPUCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-dpu-cluster",
			Namespace: "default",
		},
		Spec: opiv1alpha1.DPUClusterSpec{
			Vendor:             "nvidia",
			BFB:                "http://nvidia.com/bfb-v3.0.bfb",
			DpuFlavor:          "medium",
			NetworkOffloadMode: "ovn-kubernetes",
			VpcName:            "vpc-1",
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "worker-node-1",
			},
		},
	}

	// Set up fake client with status subresource support
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(
			&opiv1alpha1.DPUCluster{},
			&opiv1alpha1.DPUSet{},
			&opiv1alpha1.DPUService{},
		).
		Build()

	reconciler := &DPUClusterReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Log:      log.Log.WithName("test-controller"),
		Recorder: record.NewFakeRecorder(100),
	}

	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Name:      "my-dpu-cluster",
			Namespace: "default",
		},
	}

	// Run first reconcile loop (creates DPUSet and DPUService)
	_, err = reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("first reconcile failed: %v", err)
	}

	// Verify DPUSet is created
	var dpuset opiv1alpha1.DPUSet
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "my-dpu-cluster-dpuset"}, &dpuset)
	if err != nil {
		t.Fatalf("expected DPUSet to be created, got error: %v", err)
	}

	if dpuset.Spec.DPUTemplate.Spec.BFB == nil || dpuset.Spec.DPUTemplate.Spec.BFB.Name != "http://nvidia.com/bfb-v3.0.bfb" {
		t.Errorf("expected DPUSet BFB reference to be translated correctly: %+v", dpuset.Spec.DPUTemplate.Spec.BFB)
	}

	if dpuset.Spec.DPUTemplate.Spec.DPUFlavor != "medium" {
		t.Errorf("expected DPUSet flavor to be translated correctly: %s", dpuset.Spec.DPUTemplate.Spec.DPUFlavor)
	}

	// Verify DPUService is created
	var dpuservice opiv1alpha1.DPUService
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "my-dpu-cluster-dpuservice"}, &dpuservice)
	if err != nil {
		t.Fatalf("expected DPUService to be created, got error: %v", err)
	}

	// Simulate DPUSet becoming ready
	dpuset.Status.Conditions = []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Ready",
		Message:            "DPUSet is ready",
		LastTransitionTime: metav1.Now(),
	}}
	err = fakeClient.Status().Update(context.Background(), &dpuset)
	if err != nil {
		t.Fatalf("failed to update DPUSet status: %v", err)
	}

	// Simulate DPUService becoming ready
	dpuservice.Status.Conditions = []metav1.Condition{{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Ready",
		Message:            "DPUService is ready",
		LastTransitionTime: metav1.Now(),
	}}
	err = fakeClient.Status().Update(context.Background(), &dpuservice)
	if err != nil {
		t.Fatalf("failed to update DPUService status: %v", err)
	}

	// Run second reconcile loop (propagates status back to DPUCluster)
	_, err = reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("second reconcile failed: %v", err)
	}

	// Verify DPUCluster status is now Ready = true
	var updatedCluster opiv1alpha1.DPUCluster
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "my-dpu-cluster"}, &updatedCluster)
	if err != nil {
		t.Fatalf("failed to get DPUCluster: %v", err)
	}

	if !updatedCluster.Status.Ready {
		t.Errorf("expected DPUCluster.Status.Ready to be true, got false")
	}

	if updatedCluster.Status.Phase != "Ready" {
		t.Errorf("expected DPUCluster.Status.Phase to be Ready, got %s", updatedCluster.Status.Phase)
	}
}

func TestDPUClusterReconciler_Reconcile_SkipNonNvidia(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = opiv1alpha1.AddToScheme(scheme)

	cluster := &opiv1alpha1.DPUCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "intel-cluster",
			Namespace: "default",
		},
		Spec: opiv1alpha1.DPUClusterSpec{
			Vendor: "intel",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cluster).
		WithStatusSubresource(&opiv1alpha1.DPUCluster{}).
		Build()

	reconciler := &DPUClusterReconciler{
		Client:   fakeClient,
		Scheme:   scheme,
		Log:      log.Log.WithName("test-controller"),
		Recorder: record.NewFakeRecorder(10),
	}

	req := ctrl.Request{
		NamespacedName: client.ObjectKey{
			Name:      "intel-cluster",
			Namespace: "default",
		},
	}

	_, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}

	// Verify DPUSet is NOT created
	var dpuset opiv1alpha1.DPUSet
	err = fakeClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: "intel-cluster-dpuset"}, &dpuset)
	if err == nil {
		t.Errorf("expected DPUSet to NOT be created for intel vendor")
	}
}
