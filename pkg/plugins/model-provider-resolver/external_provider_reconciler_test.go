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

package model_provider_resolver

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
)

type mockProviderReader struct {
	objects map[types.NamespacedName]*inferencev1alpha1.ExternalProvider
}

func (m *mockProviderReader) Get(_ context.Context, key types.NamespacedName, obj client.Object, _ ...client.GetOption) error {
	stored, ok := m.objects[key]
	if !ok {
		return apierrors.NewNotFound(schema.GroupResource{Group: "inference.opendatahub.io", Resource: "externalproviders"}, key.Name)
	}
	*obj.(*inferencev1alpha1.ExternalProvider) = *stored.DeepCopy()
	return nil
}

func (m *mockProviderReader) List(_ context.Context, _ client.ObjectList, _ ...client.ListOption) error {
	return nil
}

func TestProviderReconciler_ValidCR(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-openai"}
	reader := &mockProviderReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalProvider{
		key: {
			ObjectMeta: metav1.ObjectMeta{Name: "my-openai", Namespace: "models"},
			Spec: inferencev1alpha1.ExternalProviderSpec{
				Provider: "openai",
				Endpoint: "api.openai.com",
				Auth: inferencev1alpha1.AuthConfig{
					Type:      "apikey",
					SecretRef: inferencev1alpha1.NameReference{Name: "openai-key"}},
			},
		},
	}}
	store := newInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getProvider(key)
	require.True(t, found)
	assert.Equal(t, "openai", info.provider)
	assert.Equal(t, "api.openai.com", info.endpoint)
	assert.Equal(t, auth.APIKey, info.auth)
	assert.Equal(t, "openai-key", info.secretName)
	assert.Equal(t, "models", info.secretNamespace)
	// openai has no special auth header default — config should not contain authHeaderName
	_, hasAuthHeader := info.config[auth.APIKeyAuthHeaderName]
	assert.False(t, hasAuthHeader, "openai provider should not have auth header default injected")
}

func TestProviderReconciler_DeletedCR(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "deleted"}
	reader := &mockProviderReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalProvider{}}

	store := newInfoStore()
	store.addOrUpdateProvider(key, &providerInfo{provider: "openai", endpoint: "api.openai.com"})

	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	_, found := store.getProvider(key)
	assert.False(t, found, "store entry should be removed on delete")
}

func TestProviderReconciler_WithConfig(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-vertex"}
	reader := &mockProviderReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalProvider{
		key: {
			ObjectMeta: metav1.ObjectMeta{Name: "my-vertex", Namespace: "models"},
			Spec: inferencev1alpha1.ExternalProviderSpec{
				Provider: "vertex-openai",
				Endpoint: "us-central1-aiplatform.googleapis.com",
				Auth: inferencev1alpha1.AuthConfig{
					Type:      "apikey",
					SecretRef: inferencev1alpha1.NameReference{Name: "vertex-key"}},
				Config: map[string]string{"project": "my-project", "location": "us-central1"},
			},
		},
	}}
	store := newInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	info, found := store.getProvider(key)
	require.True(t, found)
	assert.Equal(t, auth.APIKey, info.auth)
	assert.Equal(t, "my-project", info.config["project"])
	assert.Equal(t, "us-central1", info.config["location"])
}

func TestProviderReconciler_AuthDefaultsInjected(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-anthropic"}
	reader := &mockProviderReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalProvider{
		key: {
			ObjectMeta: metav1.ObjectMeta{Name: "my-anthropic", Namespace: "models"},
			Spec: inferencev1alpha1.ExternalProviderSpec{
				Provider: "anthropic",
				Endpoint: "api.anthropic.com",
				Auth: inferencev1alpha1.AuthConfig{
					Type:      "apikey",
					SecretRef: inferencev1alpha1.NameReference{Name: "anthropic-key"}},
			},
		},
	}}
	store := newInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	info, found := store.getProvider(key)
	require.True(t, found)
	assert.Equal(t, "x-api-key", info.config[auth.APIKeyAuthHeaderName],
		"anthropic provider should have authHeaderName default injected")
}

func TestProviderReconciler_AuthDefaultsNotOverridden(t *testing.T) {
	key := types.NamespacedName{Namespace: "models", Name: "my-anthropic"}
	reader := &mockProviderReader{objects: map[types.NamespacedName]*inferencev1alpha1.ExternalProvider{
		key: {
			ObjectMeta: metav1.ObjectMeta{Name: "my-anthropic", Namespace: "models"},
			Spec: inferencev1alpha1.ExternalProviderSpec{
				Provider: "anthropic",
				Endpoint: "api.anthropic.com",
				Auth: inferencev1alpha1.AuthConfig{
					Type:      "apikey",
					SecretRef: inferencev1alpha1.NameReference{Name: "anthropic-key"}},
				Config: map[string]string{auth.APIKeyAuthHeaderName: "custom-header"},
			},
		},
	}}
	store := newInfoStore()
	r := &externalProviderReconciler{Reader: reader, store: store}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
	require.NoError(t, err)

	info, found := store.getProvider(key)
	require.True(t, found)
	assert.Equal(t, "custom-header", info.config[auth.APIKeyAuthHeaderName],
		"user-specified config should not be overridden by defaults")
}

func TestBuildConfigWithDefaults(t *testing.T) {
	tests := []struct {
		name           string
		providerName   string
		userConfig     map[string]string
		wantHeaderName string
		wantInConfig   bool
	}{
		{
			name:           "anthropic gets x-api-key",
			providerName:   "anthropic",
			userConfig:     map[string]string{},
			wantHeaderName: "x-api-key",
			wantInConfig:   true,
		},
		{
			name:           "azure gets api-key",
			providerName:   "azure",
			userConfig:     map[string]string{},
			wantHeaderName: "api-key",
			wantInConfig:   true,
		},
		{
			name:           "azure-openai gets api-key",
			providerName:   "azure-openai",
			userConfig:     map[string]string{},
			wantHeaderName: "api-key",
			wantInConfig:   true,
		},
		{
			name:         "openai gets no default",
			providerName: "openai",
			userConfig:   map[string]string{},
			wantInConfig: false,
		},
		{
			name:           "user config overrides default",
			providerName:   "anthropic",
			userConfig:     map[string]string{auth.APIKeyAuthHeaderName: "custom"},
			wantHeaderName: "custom",
			wantInConfig:   true,
		},
		{
			name:         "nil user config uses defaults",
			providerName: "anthropic",
			userConfig:   nil,
			wantHeaderName: "x-api-key",
			wantInConfig: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := buildConfigWithDefaults(test.providerName, test.userConfig)
			headerName, exists := config[auth.APIKeyAuthHeaderName]
			assert.Equal(t, test.wantInConfig, exists)
			if test.wantInConfig {
				assert.Equal(t, test.wantHeaderName, headerName)
			}
		})
	}
}
