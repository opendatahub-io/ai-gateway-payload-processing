/*
Copyright 2026 The opendatahub.io Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package common

import (
	"slices"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
)

// AGCModelFinalizer is the finalizer ai-gateway-controller (AGC) adds to an
// ExternalModel it serves through the model's own Praxis tenant. AGC adds it
// once it serves the model there: the tenant's handoff to Praxis is steady and
// its AITenant is ready. It removes it when the tenant selects IPP again.
const AGCModelFinalizer = "inference.opendatahub.io/external-model-cleanup"

// IsAGCManaged reports whether an ExternalModel uses fields that only
// ai-gateway-controller (AGC) interprets (RHOAIENG-90800): explicit gateway
// attachments (spec.gatewayRefs) or a provider reference set that leaves IPP
// nothing to serve in the model namespace.
//
// A gatewayRefs attachment is always AGC-managed: it explicitly selects AGC
// tenant gateways, which AGC serves regardless of the model namespace, and
// AGC owns the model's status (see IsAGCAttached). A cross-namespace
// ExternalProvider reference is AGC-managed only when no same-namespace
// reference remains: AGC does not resolve cross-namespace providers yet
// (RHOAIENG-90804) — it skips them, like IPP — so a mixed model keeps
// serving through IPP on its local reference, and the edit cannot silently
// remove service.
//
// AGC does not claim models by reference shape: in namespaces selecting IPP
// it never writes model status (its cleanupUnselectedModel only removes its
// own resources), so a foreign-only model there stays IPP's to report
// (Failed, route withdrawn) even though IPP cannot serve it. This predicate
// therefore does not decide model status ownership; it excludes unservable
// shapes from the IPP data-plane store and suppresses IPP provider status
// writes when only AGC-managed models reference a provider
// (RHOAIENG-99761): AGC owns provider status in the namespaces it serves.
func IsAGCManaged(model *inferencev1alpha1.ExternalModel) bool {
	if len(model.Spec.GatewayRefs) > 0 {
		return true
	}
	localRef := false
	foreignRef := false
	for _, ref := range model.Spec.ExternalProviderRefs {
		if ref.Ref.Namespace != "" && ref.Ref.Namespace != model.Namespace {
			foreignRef = true
		} else {
			localRef = true
		}
	}
	return foreignRef && !localRef
}

// IsAGCAttached reports whether an ExternalModel explicitly attaches to AGC
// tenant gateways via spec.gatewayRefs. AGC serves attachments regardless of
// the model namespace and owns their status, so IPP must withdraw any route
// it still owns and write no status for them (RHOAIENG-99761).
func IsAGCAttached(model *inferencev1alpha1.ExternalModel) bool {
	return len(model.Spec.GatewayRefs) > 0
}

// IsAGCServed reports whether AGC serves the ExternalModel through its own
// Praxis tenant and owns its status (RHOAIENG-102557): the model carries
// AGCModelFinalizer. The default tenant's IPP serves every namespace, so it
// would otherwise also claim the models of other tenants that run on Praxis.
// The models of a tenant whose migration to Praxis is blocked carry no such
// finalizer, and IPP keeps serving them. Neither do those of a Praxis tenant
// that is not steady or ready yet: while the default tenant runs on IPP, IPP
// may still claim them until AGC serves them.
func IsAGCServed(model *inferencev1alpha1.ExternalModel) bool {
	return slices.Contains(model.GetFinalizers(), AGCModelFinalizer)
}
