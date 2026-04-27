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

package externalmodel

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"
)

const (
	labelManagedBy     = "app.kubernetes.io/managed-by"
	labelExternalModel = "inference.opendatahub.io/external-model"
	managedByValue     = "bbr-external-model-reconciler"

	defaultGatewayName      = "default-gateway"
	defaultGatewayNamespace = "openshift-ingress"
	defaultRouteTimeout     = "300s"
)

func commonLabels(modelName string) map[string]string {
	return map[string]string{
		labelManagedBy:     managedByValue,
		labelExternalModel: modelName,
	}
}

// buildHTTPRoute creates the HTTPRoute for an ExternalModel.
// Path prefix is /<namespace>/<modelName> for namespace isolation.
// Backend ref points to the ExternalProvider's Service (providerName).
// Host header is set for TLS SNI — must happen before BBR ext-proc runs.
func buildHTTPRoute(providerEndpoint, providerName, modelName, targetModel, namespace string, port int32, gatewayName, gatewayNamespace, routeTimeout string, labels map[string]string) *gatewayapiv1.HTTPRoute {
	gwNamespace := gatewayapiv1.Namespace(gatewayNamespace)
	pathType := gatewayapiv1.PathMatchPathPrefix
	pathPrefix := "/" + namespace + "/" + modelName
	headerType := gatewayapiv1.HeaderMatchExact
	gwPort := gatewayapiv1.PortNumber(port)
	timeout := gatewayapiv1.Duration(routeTimeout)

	backendRefs := []gatewayapiv1.HTTPBackendRef{
		{
			BackendRef: gatewayapiv1.BackendRef{
				BackendObjectReference: gatewayapiv1.BackendObjectReference{
					Name: gatewayapiv1.ObjectName(providerName),
					Port: &gwPort,
				},
			},
		},
	}

	filters := []gatewayapiv1.HTTPRouteFilter{
		{
			Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
				Set: []gatewayapiv1.HTTPHeader{
					{
						Name:  "Host",
						Value: providerEndpoint,
					},
				},
			},
		},
	}

	return &gatewayapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: gatewayapiv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
				ParentRefs: []gatewayapiv1.ParentReference{
					{
						Name:      gatewayapiv1.ObjectName(gatewayName),
						Namespace: &gwNamespace,
					},
				},
			},
			Rules: []gatewayapiv1.HTTPRouteRule{
				{
					Matches: []gatewayapiv1.HTTPRouteMatch{
						{
							Path: &gatewayapiv1.HTTPPathMatch{
								Type:  &pathType,
								Value: &pathPrefix,
							},
						},
					},
					BackendRefs: backendRefs,
					Filters:     filters,
					Timeouts:    &gatewayapiv1.HTTPRouteTimeouts{Request: &timeout},
				},
				{
					Matches: []gatewayapiv1.HTTPRouteMatch{
						{
							Headers: []gatewayapiv1.HTTPHeaderMatch{
								{
									Name:  "X-Gateway-Model-Name",
									Type:  &headerType,
									Value: targetModel,
								},
							},
						},
					},
					BackendRefs: backendRefs,
					Filters:     filters,
					Timeouts:    &gatewayapiv1.HTTPRouteTimeouts{Request: &timeout},
				},
			},
		},
	}
}
