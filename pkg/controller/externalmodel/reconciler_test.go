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
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
)

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
)

func TestMain(m *testing.M) {
	ctrl.SetLogger(zap.New(zap.WriteTo(os.Stderr), zap.UseDevMode(true)))

	ctx, cancel = context.WithCancel(context.Background())

	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(inferencev1alpha1.AddToScheme(scheme))
	utilruntime.Must(gatewayapiv1.Install(scheme))

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			filepath.Join("testdata", "gateway-api-crds"),
		},
		Scheme: scheme,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(err)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		panic(err)
	}

	if err := (&Reconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		GatewayName:      "test-gateway",
		GatewayNamespace: "test-gateway-ns",
	}).SetupWithManager(mgr); err != nil {
		panic(err)
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			panic(err)
		}
	}()

	code := m.Run()

	cancel()
	if err := testEnv.Stop(); err != nil {
		panic(err)
	}
	os.Exit(code)
}

// --- helpers ---

func createTestNamespace(t *testing.T) string {
	t.Helper()
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "test-em-"},
	}
	require.NoError(t, k8sClient.Create(ctx, ns))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, ns) })
	return ns.Name
}

func createExternalProvider(t *testing.T, name, namespace, endpoint string) {
	t.Helper()
	provider := &inferencev1alpha1.ExternalProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: inferencev1alpha1.ExternalProviderSpec{
			Provider: "openai",
			Endpoint: endpoint,
			Auth: inferencev1alpha1.AuthConfig{
				SecretRef: inferencev1alpha1.NameReference{Name: "dummy-secret"},
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, provider))

	provider.Status.Phase = "Ready"
	require.NoError(t, k8sClient.Status().Update(ctx, provider))
}

func newExternalModel(name, namespace, providerName, targetModel string) *inferencev1alpha1.ExternalModel {
	return &inferencev1alpha1.ExternalModel{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: inferencev1alpha1.ExternalModelSpec{
			ExternalProviderRefs: []inferencev1alpha1.ExternalProviderRef{
				{
					Ref:         inferencev1alpha1.NameReference{Name: providerName},
					TargetModel: targetModel,
					APIFormat:   "openai",
				},
			},
		},
	}
}

func waitForModelPhase(t *testing.T, name, namespace, expectedPhase string) {
	t.Helper()
	require.Eventually(t, func() bool {
		m := &inferencev1alpha1.ExternalModel{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, m); err != nil {
			return false
		}
		return m.Status.Phase == expectedPhase
	}, 10*time.Second, 100*time.Millisecond, "expected phase %s", expectedPhase)
}

// --- test cases ---

func TestReconcile_CreatesHTTPRoute(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "my-openai", ns, "api.openai.com")

	model := newExternalModel("gpt4", ns, "my-openai", "gpt-4o")
	require.NoError(t, k8sClient.Create(ctx, model))

	waitForModelPhase(t, "gpt4", ns, "Ready")

	// Verify HTTPRoute created
	hr := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "gpt4", Namespace: ns}, hr))

	// Parent ref
	require.Len(t, hr.Spec.ParentRefs, 1)
	assert.Equal(t, "test-gateway", string(hr.Spec.ParentRefs[0].Name))

	// Two rules: path-based + X-Selected-Provider header
	require.Len(t, hr.Spec.Rules, 2)
	assert.Equal(t, "/"+ns+"/gpt4", *hr.Spec.Rules[0].Matches[0].Path.Value)
	assert.Equal(t, "X-Selected-Provider", string(hr.Spec.Rules[1].Matches[0].Headers[0].Name))
	assert.Equal(t, "my-openai", hr.Spec.Rules[1].Matches[0].Headers[0].Value)

	// Backend ref points to provider's Service
	assert.Equal(t, "my-openai", string(hr.Spec.Rules[0].BackendRefs[0].Name))
	assert.Equal(t, "my-openai", string(hr.Spec.Rules[1].BackendRefs[0].Name))

	// Host header for TLS SNI
	assert.Equal(t, "api.openai.com",
		hr.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value)

	// Labels
	assert.Equal(t, managedByValue, hr.Labels[labelManagedBy])
	assert.Equal(t, "gpt4", hr.Labels[labelExternalModel])

	// OwnerReference
	require.Len(t, hr.OwnerReferences, 1)
	assert.Equal(t, "ExternalModel", hr.OwnerReferences[0].Kind)
	assert.Equal(t, "gpt4", hr.OwnerReferences[0].Name)

	// Status
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "gpt4", Namespace: ns}, model))
	require.Len(t, model.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionTrue, model.Status.Conditions[0].Status)
	assert.Equal(t, "Reconciled", model.Status.Conditions[0].Reason)
}

func TestReconcile_MissingProvider(t *testing.T) {
	ns := createTestNamespace(t)
	// Intentionally do NOT create the provider

	model := newExternalModel("orphan", ns, "nonexistent", "gpt-4o")
	require.NoError(t, k8sClient.Create(ctx, model))

	waitForModelPhase(t, "orphan", ns, "Failed")

	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "orphan", Namespace: ns}, model))
	require.Len(t, model.Status.Conditions, 1)
	assert.Equal(t, "ReconcileFailed", model.Status.Conditions[0].Reason)
	assert.Contains(t, model.Status.Conditions[0].Message, "nonexistent")
}

func TestReconcile_ProviderEndpointUpdate(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "provider-a", ns, "api.openai.com")

	model := newExternalModel("model-a", ns, "provider-a", "gpt-4o")
	require.NoError(t, k8sClient.Create(ctx, model))
	waitForModelPhase(t, "model-a", ns, "Ready")

	// Verify initial Host header
	hr := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "model-a", Namespace: ns}, hr))
	assert.Equal(t, "api.openai.com",
		hr.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value)

	// Update the provider's endpoint — cross-watch should trigger model reconcile
	provider := &inferencev1alpha1.ExternalProvider{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "provider-a", Namespace: ns}, provider))
	provider.Spec.Endpoint = "api.anthropic.com"
	require.NoError(t, k8sClient.Update(ctx, provider))

	// Wait for HTTPRoute to reflect the new endpoint
	require.Eventually(t, func() bool {
		hr = &gatewayapiv1.HTTPRoute{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "model-a", Namespace: ns}, hr); err != nil {
			return false
		}
		return hr.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value == "api.anthropic.com"
	}, 10*time.Second, 100*time.Millisecond)

	// Backend ref should still point to the same provider
	assert.Equal(t, "provider-a", string(hr.Spec.Rules[0].BackendRefs[0].Name))
}

func TestReconcile_DeleteModel(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "my-provider", ns, "api.openai.com")

	model := newExternalModel("to-delete", ns, "my-provider", "gpt-4o")
	require.NoError(t, k8sClient.Create(ctx, model))
	waitForModelPhase(t, "to-delete", ns, "Ready")

	// Verify HTTPRoute exists with correct OwnerReference
	hr := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "to-delete", Namespace: ns}, hr))
	require.Len(t, hr.OwnerReferences, 1)
	assert.Equal(t, "ExternalModel", hr.OwnerReferences[0].Kind)
	assert.True(t, *hr.OwnerReferences[0].Controller)

	// Delete model
	require.NoError(t, k8sClient.Delete(ctx, model))
	require.Eventually(t, func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "to-delete", Namespace: ns}, &inferencev1alpha1.ExternalModel{})
		return apierrors.IsNotFound(err)
	}, 10*time.Second, 100*time.Millisecond)
}

func TestReconcile_TwoModelsOneProvider(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "shared-provider", ns, "api.openai.com")

	m1 := newExternalModel("gpt4", ns, "shared-provider", "gpt-4o")
	m2 := newExternalModel("gpt35", ns, "shared-provider", "gpt-3.5-turbo")
	require.NoError(t, k8sClient.Create(ctx, m1))
	require.NoError(t, k8sClient.Create(ctx, m2))

	waitForModelPhase(t, "gpt4", ns, "Ready")
	waitForModelPhase(t, "gpt35", ns, "Ready")

	// Verify independent HTTPRoutes
	hr1 := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "gpt4", Namespace: ns}, hr1))
	hr2 := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "gpt35", Namespace: ns}, hr2))

	// Both point to the same provider Service
	assert.Equal(t, "shared-provider", string(hr1.Spec.Rules[0].BackendRefs[0].Name))
	assert.Equal(t, "shared-provider", string(hr2.Spec.Rules[0].BackendRefs[0].Name))

	// Both use X-Selected-Provider header pointing to same provider
	assert.Equal(t, "shared-provider", hr1.Spec.Rules[1].Matches[0].Headers[0].Value)
	assert.Equal(t, "shared-provider", hr2.Spec.Rules[1].Matches[0].Headers[0].Value)

	// Different path prefixes
	assert.Equal(t, "/"+ns+"/gpt4", *hr1.Spec.Rules[0].Matches[0].Path.Value)
	assert.Equal(t, "/"+ns+"/gpt35", *hr2.Spec.Rules[0].Matches[0].Path.Value)

	// Independent labels and owners
	assert.Equal(t, "gpt4", hr1.Labels[labelExternalModel])
	assert.Equal(t, "gpt35", hr2.Labels[labelExternalModel])
	assert.Equal(t, "gpt4", hr1.OwnerReferences[0].Name)
	assert.Equal(t, "gpt35", hr2.OwnerReferences[0].Name)
}

func TestReconcile_MultiProviderHTTPRoute(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "prov-openai", ns, "api.openai.com")
	createExternalProvider(t, "prov-anthropic", ns, "api.anthropic.com")

	weight80 := int32(80)
	weight20 := int32(20)
	model := &inferencev1alpha1.ExternalModel{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-model", Namespace: ns},
		Spec: inferencev1alpha1.ExternalModelSpec{
			ExternalProviderRefs: []inferencev1alpha1.ExternalProviderRef{
				{
					Ref:         inferencev1alpha1.NameReference{Name: "prov-openai"},
					TargetModel: "gpt-4o",
					APIFormat:   "openai",
					Weight:      &weight80,
				},
				{
					Ref:         inferencev1alpha1.NameReference{Name: "prov-anthropic"},
					TargetModel: "claude-3-opus",
					APIFormat:   "anthropic",
					Weight:      &weight20,
				},
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, model))

	waitForModelPhase(t, "multi-model", ns, "Ready")

	hr := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "multi-model", Namespace: ns}, hr))

	// 3 rules: path-based + 2 X-Selected-Provider headers
	require.Len(t, hr.Spec.Rules, 3)

	// Rule 1: path-based
	assert.Equal(t, "/"+ns+"/multi-model", *hr.Spec.Rules[0].Matches[0].Path.Value)

	// Rule 2: X-Selected-Provider: prov-openai
	assert.Equal(t, "X-Selected-Provider", string(hr.Spec.Rules[1].Matches[0].Headers[0].Name))
	assert.Equal(t, "prov-openai", hr.Spec.Rules[1].Matches[0].Headers[0].Value)
	assert.Equal(t, "prov-openai", string(hr.Spec.Rules[1].BackendRefs[0].Name))
	assert.Equal(t, "api.openai.com", hr.Spec.Rules[1].Filters[0].RequestHeaderModifier.Set[0].Value)

	// Rule 3: X-Selected-Provider: prov-anthropic
	assert.Equal(t, "prov-anthropic", hr.Spec.Rules[2].Matches[0].Headers[0].Value)
	assert.Equal(t, "prov-anthropic", string(hr.Spec.Rules[2].BackendRefs[0].Name))
	assert.Equal(t, "api.anthropic.com", hr.Spec.Rules[2].Filters[0].RequestHeaderModifier.Set[0].Value)
}
