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

package main

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	ctrlbuilder "sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/externalmodel"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/externalprovider"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/legacymigration"
)

// providerController returns a setup function that registers the ExternalProvider
// controller. It creates Service, ServiceEntry, and DestinationRule resources.
func providerController() func(client.Client, *ctrlbuilder.Builder) error {
	return func(c client.Client, b *ctrlbuilder.Builder) error {
		utilruntime.Must(inferencev1alpha1.AddToScheme(c.Scheme()))

		return b.
			For(&inferencev1alpha1.ExternalProvider{}).
			Named("external-provider-controller").
			Complete(&externalprovider.Reconciler{Client: c, Scheme: c.Scheme()})
	}
}

// modelController returns a setup function that registers the ExternalModel
// controller. It creates HTTPRoute resources with the specified gateway parent ref.
func modelController(gatewayName, gatewayNamespace string) func(client.Client, *ctrlbuilder.Builder) error {
	return func(c client.Client, b *ctrlbuilder.Builder) error {
		utilruntime.Must(inferencev1alpha1.AddToScheme(c.Scheme()))
		utilruntime.Must(gatewayapiv1.Install(c.Scheme()))

		reconciler := &externalmodel.Reconciler{
			Client:           c,
			Scheme:           c.Scheme(),
			GatewayName:      gatewayName,
			GatewayNamespace: gatewayNamespace,
		}

		return b.
			For(&inferencev1alpha1.ExternalModel{}).
			Named("external-model-controller").
			Watches(&inferencev1alpha1.ExternalProvider{},
				handler.EnqueueRequestsFromMapFunc(reconciler.MapProviderToModels)).
			Complete(reconciler)
	}
}

// legacyMigrationController returns a setup function that registers the legacy
// migration controller. It watches maas.opendatahub.io ExternalModel CRs and
// creates corresponding inference.opendatahub.io ExternalProvider + ExternalModel CRs.
func legacyMigrationController() func(client.Client, *ctrlbuilder.Builder) error {
	return func(c client.Client, b *ctrlbuilder.Builder) error {
		legacyObj := &unstructured.Unstructured{}
		legacyObj.SetGroupVersionKind(legacymigration.LegacyExternalModelGVK)

		return b.
			For(legacyObj).
			Named("legacy-migration").
			Complete(&legacymigration.Reconciler{Client: c})
	}
}
