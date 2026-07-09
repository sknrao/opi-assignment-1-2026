package adapter

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/opiproject/opi-nvidia-dpf-adapter/internal/dpf"
)

// status.go — companion to feature_skeleton.go (same package `adapter`).
//
// Condition ownership (arch §5, self-review Q-condition): the adapter writes a
// DISTINCT condition type it solely owns — it does NOT write the top-level
// "Ready" that OPI's own daemon/controller-manager also manages. Two
// controllers fighting over one condition type produces status flapping. The
// adapter owns "DPFReady"; a downstream aggregator (or the daemon) may fold it
// into the object's overall Ready, but only one writer touches each type.
const (
	// CondDPFReady is the adapter-owned condition on OPI objects.
	CondDPFReady = "DPFReady"

	ReasonProvisioned           = "Provisioned"
	ReasonProvisioning          = "Provisioning"
	ReasonProvisioningFailed    = "ProvisioningFailed"
	ReasonChainProgrammed       = "ChainProgrammed"
	ReasonServiceDeploying      = "ServiceDeploying"
	ReasonDPUClusterUnreach     = "DPUClusterUnreachable"
	ReasonDPFVersionUnsupported = "DPFVersionUnsupported"
	// ReasonDPFStatusSchema is surfaced when a DPF object is served and readable
	// but its status no longer carries the readiness signal the adapter keys on
	// (e.g. DPF moved DPU readiness off status.phase). This is deliberately a
	// DISTINCT reason from ReasonProvisioning so a mid-version readiness-signal
	// change is VISIBLE at the OPI layer instead of masquerading as a DPU that
	// simply never finishes provisioning (arch §12, Q3).
	ReasonDPFStatusSchema = "DPFStatusUnrecognized"
)

// mapDPUPhase turns a DPF DPU.status.phase into the adapter-owned DPFReady
// condition (arch §5 aggregation table). observedGen is stamped so stale
// status is detectable.
func mapDPUPhase(phase string, observedGen int64) metav1.Condition {
	c := metav1.Condition{
		Type:               CondDPFReady,
		ObservedGeneration: observedGen,
	}
	switch phase {
	case dpf.DPUPhaseReady:
		c.Status = metav1.ConditionTrue
		c.Reason = ReasonProvisioned
		c.Message = "DPU provisioned and joined its DPU cluster"
	case dpf.DPUPhaseError:
		c.Status = metav1.ConditionFalse
		c.Reason = ReasonProvisioningFailed
		c.Message = "DPF reported DPU provisioning error"
	case dpf.DPUPhaseInitializing, dpf.DPUPhaseProvisioning, "":
		c.Status = metav1.ConditionFalse
		c.Reason = ReasonProvisioning
		c.Message = "DPF is provisioning the DPU"
	default:
		c.Status = metav1.ConditionFalse
		c.Reason = ReasonProvisioning
		c.Message = "unrecognized DPF phase: " + phase
	}
	return c
}

// degraded builds a DPFReady=False condition for a transient/day-2 failure
// without clearing prior state — the caller must NOT overwrite an existing
// True on unrelated errors (arch §5: no stale green, but also no false-red
// flap).
func degraded(reason, msg string, observedGen int64) metav1.Condition {
	return metav1.Condition{
		Type:               CondDPFReady,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: observedGen,
	}
}
