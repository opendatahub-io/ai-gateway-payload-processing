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
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	labelManagedByAITenant = "maas.opendatahub.io/managed-by-aitenant"
	labelTenantName        = "maas.opendatahub.io/tenant-name"
	labelAIGatewayTenant   = "ai-gateway.opendatahub.io/tenant"
)

var maasAITenantGVK = schema.GroupVersionKind{Group: "maas.opendatahub.io", Version: "v1alpha1", Kind: "AITenant"}

// GatewayRef identifies the Gateway API parent for tenant-scoped HTTPRoutes.
type GatewayRef struct {
	Name      string
	Namespace string
}

// ResolveGatewayForNamespace resolves the Gateway API parent reference for
// resources in tenantNamespace using namespace labels → AITenant.status.gatewayRef.
func ResolveGatewayForNamespace(
	ctx context.Context,
	c client.Reader,
	tenantNamespace string,
	aitenantNamespace string,
	defaultTenantNamespace string,
	fallbackGatewayName string,
	fallbackGatewayNamespace string,
	discoveryEnabled bool,
) (GatewayRef, error) {
	if aitenantNamespace == "" {
		aitenantNamespace = DefaultAITenantNamespace
	}
	fallback := fallbackGatewayRef(fallbackGatewayName, fallbackGatewayNamespace)

	var ns corev1.Namespace
	if err := c.Get(ctx, client.ObjectKey{Name: tenantNamespace}, &ns); err != nil {
		if apierrors.IsNotFound(err) {
			if tenantNamespace == defaultTenantNamespace || !discoveryEnabled {
				return fallback, nil
			}
			return GatewayRef{}, fmt.Errorf("namespace %s not found", tenantNamespace)
		}
		return GatewayRef{}, err
	}

	if isAITenantManagedNamespace(ns.Labels) {
		ref, err := gatewayFromAITenant(ctx, c, ns.Labels, aitenantNamespace)
		if err != nil {
			if tenantNamespace == defaultTenantNamespace {
				return fallback, nil
			}
			return GatewayRef{}, err
		}
		return ref, nil
	}

	if tenantNamespace == defaultTenantNamespace || !discoveryEnabled {
		return fallback, nil
	}
	if namespaceHasTenantDiscoveryLabel(ns.Labels) {
		return GatewayRef{}, fmt.Errorf(
			"namespace %s is tenant-discovered but not AITenant-managed (missing %s=true)",
			tenantNamespace, labelManagedByAITenant,
		)
	}
	return fallback, nil
}

func isAITenantManagedNamespace(labels map[string]string) bool {
	return labels != nil && labels[labelManagedByAITenant] == "true"
}

func gatewayFromAITenant(
	ctx context.Context,
	c client.Reader,
	labels map[string]string,
	aitenantNamespace string,
) (GatewayRef, error) {
	tenantName := labels[labelTenantName]
	if tenantName == "" {
		tenantName = labels[labelAIGatewayTenant]
	}
	if tenantName == "" {
		return GatewayRef{}, fmt.Errorf("AITenant-managed namespace is missing %s", labelTenantName)
	}

	aitenant := &unstructured.Unstructured{}
	aitenant.SetGroupVersionKind(maasAITenantGVK)
	key := client.ObjectKey{Name: tenantName, Namespace: aitenantNamespace}
	if err := c.Get(ctx, key, aitenant); err != nil {
		return GatewayRef{}, fmt.Errorf("get AITenant %s/%s: %w", key.Namespace, key.Name, err)
	}

	name, _, _ := unstructured.NestedString(aitenant.Object, "status", "gatewayRef", "name")
	namespace, _, _ := unstructured.NestedString(aitenant.Object, "status", "gatewayRef", "namespace")
	if name == "" || namespace == "" {
		return GatewayRef{}, fmt.Errorf("AITenant %s/%s status.gatewayRef is not ready", key.Namespace, key.Name)
	}
	return GatewayRef{Name: name, Namespace: namespace}, nil
}

func namespaceHasTenantDiscoveryLabel(labels map[string]string) bool {
	return labels[labelAIGatewayTenant] != "" || labels[labelManagedByAITenant] == "true"
}

func fallbackGatewayRef(name, namespace string) GatewayRef {
	if name == "" || namespace == "" {
		return GatewayRef{}
	}
	return GatewayRef{Name: name, Namespace: namespace}
}
