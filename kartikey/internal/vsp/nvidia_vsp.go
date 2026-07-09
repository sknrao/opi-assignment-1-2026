package vsp

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opiv1 "github.com/opiproject/opi-nvidia-dpf-adapter/api/opi/v1"
)

// ErrNotProvisioned is returned by Init when DPF has not yet finished
// provisioning; the daemon treats it as a retriable backoff, not a hard fail.
var ErrNotProvisioned = errors.New("dpu not yet provisioned by DPF")

// NvidiaVSP is the thin, per-DPU implementation of VendorPlugin (arch pattern
// (e)). It holds NO DPU-cluster credentials and does NOT talk to DPF. Its whole
// job is to (1) satisfy the daemon's gRPC handshake, (2) record intent it can't
// satisfy synchronously onto its DataProcessingUnit, and (3) read mirrored DPF
// status back out and hand it to the daemon. The cluster-scoped dpuf-adapter
// controller does the privileged work (arch §6).
type NvidiaVSP struct {
	// Client is a HOST-cluster client scoped by RBAC to patch only this node's
	// DataProcessingUnit (arch §6: node pods hold nothing privileged).
	Client client.Client
	// DPURef identifies the DataProcessingUnit for the DPU this VSP fronts.
	DPURef types.NamespacedName
	// Log is the structured logger (no fmt.Println anywhere).
	Log logr.Logger
}

// compile-time assertion that NvidiaVSP satisfies the full contract.
var _ VendorPlugin = (*NvidiaVSP)(nil)

// dpu fetches this VSP's backing DataProcessingUnit.
func (v *NvidiaVSP) dpu(ctx context.Context) (*opiv1.DataProcessingUnit, error) {
	d := &opiv1.DataProcessingUnit{}
	if err := v.Client.Get(ctx, v.DPURef, d); err != nil {
		return nil, fmt.Errorf("get DataProcessingUnit %s: %w", v.DPURef, err)
	}
	return d, nil
}

// recordIntent stamps an intent annotation for the cluster controller to act
// on, without the VSP itself touching DPF.
func (v *NvidiaVSP) recordIntent(ctx context.Context, intent string) error {
	d, err := v.dpu(ctx)
	if err != nil {
		return err
	}
	patch := client.MergeFrom(d.DeepCopy())
	if d.Annotations == nil {
		d.Annotations = map[string]string{}
	}
	d.Annotations[opiv1.IntentAnnotation] = intent
	if err := v.Client.Patch(ctx, d, patch); err != nil {
		return fmt.Errorf("patch intent %q onto %s: %w", intent, v.DPURef, err)
	}
	v.Log.Info("recorded adapter intent", "intent", intent, "dpu", v.DPURef.String())
	return nil
}

// Init returns the DPF-provisioned datapath endpoint. It does not provision —
// it reads the endpoint the cluster controller published as an annotation on
// the DataProcessingUnit (the real CR has no status field for it), and hands it
// to the daemon, whose success flips the daemon-owned "Ready" (arch §3/§4-i/§5).
func (v *NvidiaVSP) Init(ctx context.Context) (InitResponse, error) {
	d, err := v.dpu(ctx)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Daemon may call Init before the CR exists; ask for provisioning.
			return InitResponse{Ready: false, Reason: "DataProcessingUnitPending"}, nil
		}
		return InitResponse{}, err
	}

	// Until the adapter publishes the endpoint, request provisioning (idempotent)
	// and tell the daemon to back off rather than returning a hard error.
	endpoint := d.Annotations[opiv1.EndpointAnnotation]
	if endpoint == "" {
		if ierr := v.recordIntent(ctx, "provision"); ierr != nil {
			v.Log.Error(ierr, "failed to record provision intent", "dpu", v.DPURef.String())
		}
		v.Log.Info("Init: datapath endpoint not published yet, backing off", "dpu", v.DPURef.String())
		return InitResponse{Ready: false, Reason: "Provisioning"}, nil
	}

	v.Log.Info("Init: DPU ready", "dpu", v.DPURef.String(), "endpoint", endpoint)
	return InitResponse{DataplaneEndpoint: endpoint, Ready: true}, nil
}

// GetDevices reports the DPU's device(s). DPF owns the true inventory; the VSP
// surfaces the product identity the daemon already detected (spec.dpuProductName).
func (v *NvidiaVSP) GetDevices(ctx context.Context) ([]string, error) {
	d, err := v.dpu(ctx)
	if err != nil {
		return nil, err
	}
	if d.Spec.DpuProductName == "" {
		return nil, nil
	}
	return []string{d.Spec.DpuProductName}, nil
}

// SetNumVfs is declarative on the DPF side (a DPUFlavor field), so the VSP just
// records the desired count as intent; the controller reconciles it.
func (v *NvidiaVSP) SetNumVfs(ctx context.Context, count int32) error {
	return v.recordIntent(ctx, fmt.Sprintf("set-num-vfs=%d", count))
}

// CreateNetworkFunction — DPF delivers NFs via DPUService and wires the bridge
// declaratively; record intent (bridge wiring rides in nf.BridgeID), don't
// imperatively poke the DPU. This replaces the non-existent BridgePort RPC —
// the real proto folds bridge_id into NFRequest (arch §3).
func (v *NvidiaVSP) CreateNetworkFunction(ctx context.Context, nf NetworkFunction) error {
	if nf.Input == "" {
		return errors.New("CreateNetworkFunction: empty input port")
	}
	return v.recordIntent(ctx, fmt.Sprintf("nf:%s->%s@%s", nf.Input, nf.Output, nf.BridgeID))
}

// DeleteNetworkFunction records teardown intent.
func (v *NvidiaVSP) DeleteNetworkFunction(ctx context.Context, nf NetworkFunction) error {
	return v.recordIntent(ctx, "delete-nf:"+nf.Input)
}

// SetDpuNetworkConfig — handled declaratively by DPF; record intent.
func (v *NvidiaVSP) SetDpuNetworkConfig(ctx context.Context, cfg DpuNetworkConfig) error {
	return v.recordIntent(ctx, fmt.Sprintf("dpunetwork-accelerated=%t", cfg.IsAccelerated))
}

// Ping is the HeartbeatService liveness probe. The thin VSP holds no DPF state,
// so it answers as a simple liveness responder (echoing the timestamp); real
// DPF / DPU-cluster health surfaces through the DataProcessingUnit conditions
// the adapter mirrors, not through this RPC (arch §4-iii).
func (v *NvidiaVSP) Ping(_ context.Context, req PingRequest) (PingResponse, error) {
	return PingResponse{Timestamp: req.Timestamp, ResponderID: "nvidia-vsp", Healthy: true}, nil
}
