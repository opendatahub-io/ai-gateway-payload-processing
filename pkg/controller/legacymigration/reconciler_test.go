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

package legacymigration

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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

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

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "..", "test", "testdata", "crds", "legacy"),
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

	legacyObj := &unstructured.Unstructured{}
	legacyObj.SetGroupVersionKind(LegacyExternalModelGVK)
	if err := ctrl.NewControllerManagedBy(mgr).
		For(legacyObj).
		Named("legacy-migration").
		Complete(&Reconciler{Client: mgr.GetClient()}); err != nil {
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
		ObjectMeta: metav1.ObjectMeta{GenerateName: "test-lm-"},
	}
	require.NoError(t, k8sClient.Create(ctx, ns))
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, ns) })
	return ns.Name
}

func createLegacyExternalModel(t *testing.T, name, namespace, provider, endpoint, targetModel, credName string) {
	t.Helper()
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "maas.opendatahub.io/v1alpha1",
		"kind":       "ExternalModel",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"provider":    provider,
			"endpoint":    endpoint,
			"targetModel": targetModel,
			"credentialRef": map[string]any{
				"name": credName,
			},
		},
	}}
	require.NoError(t, k8sClient.Create(ctx, obj))
}

func waitForNewProvider(t *testing.T, name, namespace string) *inferencev1alpha1.ExternalProvider {
	t.Helper()
	var provider *inferencev1alpha1.ExternalProvider
	require.Eventually(t, func() bool {
		p := &inferencev1alpha1.ExternalProvider{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, p); err != nil {
			return false
		}
		provider = p
		return true
	}, 10*time.Second, 100*time.Millisecond, "expected ExternalProvider %s/%s to be created", namespace, name)
	return provider
}

func waitForNewModel(t *testing.T, name, namespace string) *inferencev1alpha1.ExternalModel {
	t.Helper()
	var model *inferencev1alpha1.ExternalModel
	require.Eventually(t, func() bool {
		m := &inferencev1alpha1.ExternalModel{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, m); err != nil {
			return false
		}
		model = m
		return true
	}, 10*time.Second, 100*time.Millisecond, "expected ExternalModel %s/%s to be created", namespace, name)
	return model
}

// --- test cases ---

func TestReconcile_CreatesNewCRs(t *testing.T) {
	ns := createTestNamespace(t)
	createLegacyExternalModel(t, "my-openai", ns, "openai", "api.openai.com", "gpt-4o", "openai-key")

	provider := waitForNewProvider(t, "my-openai", ns)
	assert.Equal(t, "openai", provider.Spec.Provider)
	assert.Equal(t, "api.openai.com", provider.Spec.Endpoint)
	assert.Equal(t, "simple", provider.Spec.Auth.Type)
	assert.Equal(t, "openai-key", provider.Spec.Auth.SecretRef.Name)
	assert.Equal(t, managedByValue, provider.Labels[labelManagedBy])
	assert.Equal(t, "my-openai", provider.Labels[labelMigratedFrom])

	model := waitForNewModel(t, "my-openai", ns)
	require.Len(t, model.Spec.ExternalProviderRefs, 1)
	assert.Equal(t, "my-openai", model.Spec.ExternalProviderRefs[0].Ref.Name)
	assert.Equal(t, "gpt-4o", model.Spec.ExternalProviderRefs[0].TargetModel)
	assert.Equal(t, "openai-chat", model.Spec.ExternalProviderRefs[0].APIFormat)
	assert.Equal(t, managedByValue, model.Labels[labelManagedBy])

	// OwnerReference set on both new CRs pointing to the legacy CR
	require.Len(t, provider.OwnerReferences, 1)
	assert.Equal(t, "ExternalModel", provider.OwnerReferences[0].Kind)
	assert.Equal(t, "my-openai", provider.OwnerReferences[0].Name)
	assert.True(t, *provider.OwnerReferences[0].Controller)

	require.Len(t, model.OwnerReferences, 1)
	assert.Equal(t, "ExternalModel", model.OwnerReferences[0].Kind)
	assert.Equal(t, "my-openai", model.OwnerReferences[0].Name)
}

func TestReconcile_AnthropicProvider(t *testing.T) {
	ns := createTestNamespace(t)
	createLegacyExternalModel(t, "my-claude", ns, "anthropic", "api.anthropic.com", "claude-sonnet-4-5-20241022", "anthropic-key")

	provider := waitForNewProvider(t, "my-claude", ns)
	assert.Equal(t, "anthropic", provider.Spec.Provider)

	model := waitForNewModel(t, "my-claude", ns)
	assert.Equal(t, "messages", model.Spec.ExternalProviderRefs[0].APIFormat)
	assert.Equal(t, "claude-sonnet-4-5-20241022", model.Spec.ExternalProviderRefs[0].TargetModel)
}

func TestReconcile_UpdateLegacyCR(t *testing.T) {
	ns := createTestNamespace(t)
	createLegacyExternalModel(t, "updatable", ns, "openai", "api.openai.com", "gpt-4o", "key1")

	waitForNewProvider(t, "updatable", ns)
	waitForNewModel(t, "updatable", ns)

	// Update the legacy CR's endpoint
	old := &unstructured.Unstructured{}
	old.SetGroupVersionKind(LegacyExternalModelGVK)
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "updatable", Namespace: ns}, old))
	_ = unstructured.SetNestedField(old.Object, "api.anthropic.com", "spec", "endpoint")
	require.NoError(t, k8sClient.Update(ctx, old))

	// Wait for the new provider to reflect the updated endpoint
	require.Eventually(t, func() bool {
		p := &inferencev1alpha1.ExternalProvider{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "updatable", Namespace: ns}, p); err != nil {
			return false
		}
		return p.Spec.Endpoint == "api.anthropic.com"
	}, 10*time.Second, 100*time.Millisecond)
}

func TestReconcile_DeleteLegacyCR(t *testing.T) {
	ns := createTestNamespace(t)
	createLegacyExternalModel(t, "to-delete", ns, "openai", "api.openai.com", "gpt-4o", "key1")

	waitForNewProvider(t, "to-delete", ns)
	waitForNewModel(t, "to-delete", ns)

	// Delete the legacy CR
	old := &unstructured.Unstructured{}
	old.SetGroupVersionKind(LegacyExternalModelGVK)
	old.SetName("to-delete")
	old.SetNamespace(ns)
	require.NoError(t, k8sClient.Delete(ctx, old))

	// Verify legacy CR is gone
	require.Eventually(t, func() bool {
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(LegacyExternalModelGVK)
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "to-delete", Namespace: ns}, obj)
		return apierrors.IsNotFound(err)
	}, 10*time.Second, 100*time.Millisecond)

	// New CRs still exist in envtest (no GC controller). In a real cluster,
	// OwnerReference cascade would delete them when the legacy CR is removed.
	p := &inferencev1alpha1.ExternalProvider{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "to-delete", Namespace: ns}, p))
	assert.Equal(t, "to-delete", p.OwnerReferences[0].Name)
}

func TestReconcile_DoesNotOverwriteManualCRs(t *testing.T) {
	ns := createTestNamespace(t)

	// Create a new ExternalProvider manually (NOT managed by migration)
	manual := &inferencev1alpha1.ExternalProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "manual-provider", Namespace: ns},
		Spec: inferencev1alpha1.ExternalProviderSpec{
			Provider: "openai",
			Endpoint: "manual.openai.com",
			Auth: inferencev1alpha1.AuthConfig{
				Type:      "simple",
				SecretRef: inferencev1alpha1.NameReference{Name: "manual-key"},
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, manual))

	// Create a legacy CR with the same name
	createLegacyExternalModel(t, "manual-provider", ns, "openai", "api.openai.com", "gpt-4o", "other-key")

	// Wait for reconcile to run, then verify the manually created provider was NOT overwritten
	require.Eventually(t, func() bool {
		// The new ExternalModel should be created (reconciler ran)
		m := &inferencev1alpha1.ExternalModel{}
		return k8sClient.Get(ctx, types.NamespacedName{Name: "manual-provider", Namespace: ns}, m) == nil
	}, 10*time.Second, 100*time.Millisecond, "expected reconciler to create ExternalModel")

	p := &inferencev1alpha1.ExternalProvider{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "manual-provider", Namespace: ns}, p))
	assert.Equal(t, "manual.openai.com", p.Spec.Endpoint, "migration should not overwrite manually created provider")
}

func TestMapProviderToAPIFormat(t *testing.T) {
	tests := []struct {
		provider  string
		apiFormat string
	}{
		{"openai", "openai-chat"},
		{"anthropic", "messages"},
		{"azure", "openai-chat"},
		{"azure-openai", "openai-chat"},
		{"vertex-openai", "openai-chat"},
		{"bedrock", "openai-chat"},
		{"aws-bedrock", "openai-chat"},
		{"unknown", "openai-chat"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			assert.Equal(t, tt.apiFormat, mapProviderToAPIFormat(tt.provider))
		})
	}
}
