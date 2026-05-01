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

package controller

import (
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/bbr/framework"
	"sigs.k8s.io/gateway-api-inference-extension/pkg/epp/framework/interface/plugin"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/externalmodel"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/externalprovider"
)

const ControllerPluginType = "external-model-controller"

type controllerConfig struct {
	GatewayName      string `json:"gatewayName,omitempty"`
	GatewayNamespace string `json:"gatewayNamespace,omitempty"`
	RouteTimeout     string `json:"routeTimeout,omitempty"`
}

// ControllerFactory registers the ExternalProvider and ExternalModel reconcilers
// within the BBR process. This plugin has no request/response processing — it only
// sets up the control-plane reconcilers that create Istio networking resources.
func ControllerFactory(name string, rawConfig json.RawMessage, handle framework.Handle) (framework.BBRPlugin, error) {
	var config controllerConfig
	if len(rawConfig) > 0 {
		if err := json.Unmarshal(rawConfig, &config); err != nil {
			return nil, fmt.Errorf("failed to parse controller plugin config: %w", err)
		}
	}

	client := handle.Client()

	// ExternalProvider reconciler
	managedByPredicate, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{
		MatchLabels: map[string]string{"app.kubernetes.io/managed-by": "bbr-external-provider-reconciler"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create label predicate: %w", err)
	}

	providerReconciler := &externalprovider.Reconciler{
		Client: client,
		Scheme: client.Scheme(),
	}
	if err := handle.ReconcilerBuilder().
		For(&inferencev1alpha1.ExternalProvider{}).
		Owns(&corev1.Service{}, builder.WithPredicates(managedByPredicate)).
		Named("external-provider-reconciler").
		Complete(providerReconciler); err != nil {
		return nil, fmt.Errorf("failed to set up ExternalProvider reconciler: %w", err)
	}

	// ExternalModel reconciler
	modelReconciler := &externalmodel.Reconciler{
		Client:           client,
		Scheme:           client.Scheme(),
		GatewayName:      config.GatewayName,
		GatewayNamespace: config.GatewayNamespace,
		RouteTimeout:     config.RouteTimeout,
	}
	if err := handle.ReconcilerBuilder().
		For(&inferencev1alpha1.ExternalModel{}).
		Owns(&gatewayapiv1.HTTPRoute{}).
		Watches(&inferencev1alpha1.ExternalProvider{},
			handler.EnqueueRequestsFromMapFunc(modelReconciler.MapProviderToModels)).
		Named("external-model-reconciler").
		Complete(modelReconciler); err != nil {
		return nil, fmt.Errorf("failed to set up ExternalModel reconciler: %w", err)
	}

	return &controllerPlugin{
		typedName: plugin.TypedName{Type: ControllerPluginType, Name: name},
	}, nil
}

// controllerPlugin is a no-op plugin — the reconcilers do the work.
type controllerPlugin struct {
	typedName plugin.TypedName
}

func (p *controllerPlugin) TypedName() plugin.TypedName {
	return p.typedName
}
