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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var testScheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
}

func TestResolveGatewayForNamespace_AITenantManaged(t *testing.T) {
	ctx := context.Background()
	const tenantNS = "ai-tenant-redteam"

	aitenant := &unstructured.Unstructured{}
	aitenant.SetGroupVersionKind(maasAITenantGVK)
	aitenant.SetName("redteam")
	aitenant.SetNamespace(DefaultAITenantNamespace)
	if err := unstructured.SetNestedField(aitenant.Object, "redteam-gateway", "status", "gatewayRef", "name"); err != nil {
		t.Fatalf("set gateway name: %v", err)
	}
	if err := unstructured.SetNestedField(aitenant.Object, "openshift-ingress", "status", "gatewayRef", "namespace"); err != nil {
		t.Fatalf("set gateway namespace: %v", err)
	}

	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: tenantNS,
			Labels: map[string]string{
				labelManagedByAITenant: "true",
				labelTenantName:        "redteam",
			},
		},
	}

	c := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(aitenant, ns).Build()
	ref, err := ResolveGatewayForNamespace(ctx, c, tenantNS, DefaultAITenantNamespace, DefaultTenantNamespace, DefaultGatewayName, DefaultGatewayNamespace, true)
	if err != nil {
		t.Fatalf("ResolveGatewayForNamespace() error = %v", err)
	}
	if ref.Name != "redteam-gateway" || ref.Namespace != "openshift-ingress" {
		t.Fatalf("ResolveGatewayForNamespace() = %#v, want tenant gateway", ref)
	}
}

func TestResolveGatewayForNamespace_DefaultTenantFallback(t *testing.T) {
	ctx := context.Background()
	c := fake.NewClientBuilder().WithScheme(testScheme).Build()
	ref, err := ResolveGatewayForNamespace(ctx, c, DefaultTenantNamespace, DefaultAITenantNamespace, DefaultTenantNamespace, DefaultGatewayName, DefaultGatewayNamespace, true)
	if err != nil {
		t.Fatalf("ResolveGatewayForNamespace() error = %v", err)
	}
	if ref.Name != DefaultGatewayName || ref.Namespace != DefaultGatewayNamespace {
		t.Fatalf("ResolveGatewayForNamespace() = %#v, want default gateway fallback", ref)
	}
}

func TestResolveGatewayForNamespace_DiscoveredNamespaceWithoutTenantName(t *testing.T) {
	ctx := context.Background()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "ai-tenant-new",
			Labels: map[string]string{labelManagedByAITenant: "true"},
		},
	}
	c := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(ns).Build()
	_, err := ResolveGatewayForNamespace(ctx, c, ns.Name, DefaultAITenantNamespace, DefaultTenantNamespace, DefaultGatewayName, DefaultGatewayNamespace, true)
	if err == nil {
		t.Fatal("expected error when AITenant-managed namespace is missing tenant-name label")
	}
}
