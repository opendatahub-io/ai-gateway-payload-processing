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

// resolvedProvider holds the provider info needed to build HTTPRoute rules.
type resolvedProvider struct {
	Name     string // ExternalProvider CR name (used as Service name and header match value)
	Endpoint string // FQDN for Host header (TLS SNI)
}

// buildHTTPRoute creates the HTTPRoute for an ExternalModel with one or more providers.
// Rule 1: path-based match (/<namespace>/<modelName>) for Kuadrant compatibility.
// Per-provider rules: header match (X-Selected-Provider: <name>) with provider-specific
// backend ref and Host header for TLS SNI.
func buildHTTPRoute(modelName, namespace string, providers []resolvedProvider, port int32, gatewayName, gatewayNamespace, routeTimeout string, labels map[string]string) *gatewayapiv1.HTTPRoute {
	gwNamespace := gatewayapiv1.Namespace(gatewayNamespace)
	pathType := gatewayapiv1.PathMatchPathPrefix
	pathPrefix := "/" + namespace + "/" + modelName
	headerType := gatewayapiv1.HeaderMatchExact
	gwPort := gatewayapiv1.PortNumber(port)
	timeout := gatewayapiv1.Duration(routeTimeout)

	// First provider is used as the default backend for the path-based rule
	defaultProvider := providers[0]

	defaultBackend := []gatewayapiv1.HTTPBackendRef{{
		BackendRef: gatewayapiv1.BackendRef{
			BackendObjectReference: gatewayapiv1.BackendObjectReference{
				Name: gatewayapiv1.ObjectName(defaultProvider.Name),
				Port: &gwPort,
			},
		},
	}}

	defaultFilters := []gatewayapiv1.HTTPRouteFilter{{
		Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
		RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
			Set: []gatewayapiv1.HTTPHeader{{
				Name:  "Host",
				Value: defaultProvider.Endpoint,
			}},
		},
	}}

	rules := []gatewayapiv1.HTTPRouteRule{
		{
			Matches: []gatewayapiv1.HTTPRouteMatch{{
				Path: &gatewayapiv1.HTTPPathMatch{
					Type:  &pathType,
					Value: &pathPrefix,
				},
			}},
			BackendRefs: defaultBackend,
			Filters:     defaultFilters,
			Timeouts:    &gatewayapiv1.HTTPRouteTimeouts{Request: &timeout},
		},
	}

	// Per-provider rules matched by X-Selected-Provider header (set by BBR)
	for _, p := range providers {
		rules = append(rules, gatewayapiv1.HTTPRouteRule{
			Matches: []gatewayapiv1.HTTPRouteMatch{{
				Headers: []gatewayapiv1.HTTPHeaderMatch{{
					Name:  "X-Selected-Provider",
					Type:  &headerType,
					Value: p.Name,
				}},
			}},
			BackendRefs: []gatewayapiv1.HTTPBackendRef{{
				BackendRef: gatewayapiv1.BackendRef{
					BackendObjectReference: gatewayapiv1.BackendObjectReference{
						Name: gatewayapiv1.ObjectName(p.Name),
						Port: &gwPort,
					},
				},
			}},
			Filters: []gatewayapiv1.HTTPRouteFilter{{
				Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
				RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
					Set: []gatewayapiv1.HTTPHeader{{
						Name:  "Host",
						Value: p.Endpoint,
					}},
				},
			}},
			Timeouts: &gatewayapiv1.HTTPRouteTimeouts{Request: &timeout},
		})
	}

	return &gatewayapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: gatewayapiv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
				ParentRefs: []gatewayapiv1.ParentReference{{
					Name:      gatewayapiv1.ObjectName(gatewayName),
					Namespace: &gwNamespace,
				}},
			},
			Rules: rules,
		},
	}
}
