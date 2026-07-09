// Package v1 contains local Go types for the OPI dpu-operator public CRDs that
// the NVIDIA/DPF adapter reads and writes status on.
//
// DESIGN CHOICE (extend vs. new CRD): we deliberately DO NOT introduce a new
// adapter-owned CRD for user intent. Per the architecture doc, "maximizing
// reuse" cuts two ways — reuse DPF underneath, but also reuse OPI's *existing*
// public API on top so NVIDIA users write the same CRs as Intel/Marvell users.
// So the adapter's OPI-facing objects are the real ones:
//
//   - ServiceFunctionChain  (config.openshift.io/v1) — user-authored SFC intent
//   - DataProcessingUnit     (config.openshift.io/v1) — created BY the daemon;
//     the adapter only mirrors provisioning status + the datapath endpoint here.
//
// These are LOCAL MIRRORS of the upstream types in
// github.com/openshift/dpu-operator (group config.openshift.io/v1). Only the
// fields this adapter touches are modeled; comments name the real source. We
// mirror rather than import so the skeleton compiles without dragging the whole
// dpu-operator module graph — swapping these for the upstream import is a
// one-line change in each consumer.
package v1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the group/version for the mirrored OPI CRDs. It matches
	// the real dpu-operator, which registers under config.openshift.io/v1
	// (not a bespoke "opi" group).
	GroupVersion = schema.GroupVersion{Group: "config.openshift.io", Version: "v1"}

	// SchemeBuilder registers the mirrored types with a runtime.Scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the mirrored types to a scheme; used from cmd/main.go.
	AddToScheme = SchemeBuilder.AddToScheme
)
