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

package externalprovider

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	labelManagedBy        = "app.kubernetes.io/managed-by"
	labelExternalProvider = "inference.opendatahub.io/external-provider"
	managedByValue        = "bbr-external-provider-reconciler"
)

func commonLabels(providerName string) map[string]string {
	return map[string]string{
		labelManagedBy:        managedByValue,
		labelExternalProvider: providerName,
	}
}

// buildService creates a Kubernetes ExternalName Service that maps an in-cluster
// DNS name to the external FQDN.
func buildService(endpoint, name, namespace string, port int32, labels map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: endpoint,
			Ports: []corev1.ServicePort{
				{
					Port:       port,
					TargetPort: intstr.FromInt32(port),
				},
			},
		},
	}
}

// buildServiceEntry creates an Istio ServiceEntry that registers the external
// FQDN in the mesh service registry.
func buildServiceEntry(endpoint, name, namespace string, port int32, labels map[string]string) *unstructured.Unstructured {
	se := &unstructured.Unstructured{}
	se.SetAPIVersion("networking.istio.io/v1")
	se.SetKind("ServiceEntry")
	se.SetName(name)
	se.SetNamespace(namespace)
	se.SetLabels(labels)

	se.Object["spec"] = map[string]any{
		"hosts":      []any{endpoint},
		"location":   "MESH_EXTERNAL",
		"resolution": "DNS",
		"ports": []any{
			map[string]any{
				"number":   int64(port),
				"name":     "https",
				"protocol": "HTTPS",
			},
		},
	}
	return se
}

// buildDestinationRule creates an Istio DestinationRule that configures TLS
// origination (mode SIMPLE) for the external host.
func buildDestinationRule(endpoint, name, namespace string, labels map[string]string) *unstructured.Unstructured {
	dr := &unstructured.Unstructured{}
	dr.SetAPIVersion("networking.istio.io/v1")
	dr.SetKind("DestinationRule")
	dr.SetName(name)
	dr.SetNamespace(namespace)
	dr.SetLabels(labels)

	dr.Object["spec"] = map[string]any{
		"host": endpoint,
		"trafficPolicy": map[string]any{
			"tls": map[string]any{
				"mode": "SIMPLE",
			},
		},
	}
	return dr
}
