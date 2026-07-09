// Package vsp defines the OPI VendorPlugin contract and the thin NVIDIA
// implementation of it.
//
// LOCAL MIRROR: the real contract in github.com/openshift/dpu-operator is a
// gRPC service set defined in dpu-api/api.proto — FIVE services
// (LifeCycleService, DeviceService, NetworkFunctionService,
// DpuNetworkConfigService, HeartbeatService) exposing SEVEN RPCs, served on the
// unix socket /var/run/dpu-daemon/vendor-plugin/vendor-plugin.sock with
// generated protobuf request/response types. We mirror the *shape* as a plain
// Go interface with plain structs so the skeleton compiles without protoc and
// without importing dpu-operator. The 7 methods correspond 1:1 to the 7 RPCs
// the vendor-neutral daemon's VendorPlugin client calls (arch requirement 3).
//
// NOTE: there is deliberately NO BridgePort RPC — the real proto folds bridge
// wiring into NFRequest.bridge_id on CreateNetworkFunction. This mirror was
// corrected against the upstream dpu-api/api.proto (verified via gh, 2026-07-04):
// an earlier draft invented a BridgePortService and omitted HeartbeatService.
package vsp

import "context"

// --- mirrored request/response types (subset of the real protobuf) ---

// InitResponse mirrors the LifeCycleService.Init reply (real: IpPort{ip,port}).
// The Ready/Reason fields are an adapter-side convenience: DPF discovers the
// endpoint AFTER provisioning, so the NVIDIA VSP fills DataplaneEndpoint from
// the DataProcessingUnit endpoint annotation and reports Ready=false while DPF
// is still provisioning (arch §3, Init row).
type InitResponse struct {
	// DataplaneEndpoint is "ip:port"; empty means not yet provisioned.
	DataplaneEndpoint string
	// Ready is false while DPF is still provisioning; the daemon backs off.
	Ready bool
	// Reason carries a DPF-derived reason when not Ready (surfaced upward).
	Reason string
}

// NetworkFunction mirrors the NetworkFunctionService NFRequest
// (input, output, bridge_id). Bridge wiring rides here on BridgeID — the real
// proto has no separate BridgePort RPC.
type NetworkFunction struct {
	Input    string // ingress port reference (proto: input)
	Output   string // egress port reference (proto: output)
	BridgeID string // bridge the NF attaches to (proto: bridge_id)
}

// DpuNetworkConfig mirrors the DpuNetworkConfigService DpuNetworkConfigRequest.
type DpuNetworkConfig struct {
	IsAccelerated bool // proto: is_accelerated
}

// PingRequest / PingResponse mirror HeartbeatService.Ping — the health-check
// RPC the daemon uses to probe VSP/DPF liveness (arch §4-iii failure path).
type PingRequest struct {
	Timestamp int64
	SenderID  string
}

type PingResponse struct {
	Timestamp   int64
	ResponderID string
	Healthy     bool
}

// VendorPlugin is the interface the vendor-neutral daemon programs against.
// Mirrors dpu-operator's VendorPlugin client (7 RPCs across 5 services). A real
// gRPC server would wrap an implementation of this; here it is the seam the
// NvidiaVSP satisfies.
type VendorPlugin interface {
	// LifeCycleService.
	Init(ctx context.Context) (InitResponse, error)

	// DeviceService.
	GetDevices(ctx context.Context) ([]string, error)
	SetNumVfs(ctx context.Context, count int32) error

	// NetworkFunctionService (bridge wiring rides in NetworkFunction.BridgeID).
	CreateNetworkFunction(ctx context.Context, nf NetworkFunction) error
	DeleteNetworkFunction(ctx context.Context, nf NetworkFunction) error

	// DpuNetworkConfigService.
	SetDpuNetworkConfig(ctx context.Context, cfg DpuNetworkConfig) error

	// HeartbeatService.
	Ping(ctx context.Context, req PingRequest) (PingResponse, error)
}
