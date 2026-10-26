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
	"k8s.io/apimachinery/pkg/api/meta"
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
	ctrlcommon "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/common"
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
			filepath.Join("..", "..", "..", "test", "testdata", "crds", "gateway-api"),
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
	createExternalProviderWithAnnotations(t, name, namespace, endpoint, nil)
}

func createExternalProviderWithAnnotations(t *testing.T, name, namespace, endpoint string, annotations map[string]string) {
	t.Helper()
	provider := &inferencev1alpha1.ExternalProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: annotations,
		},
		Spec: inferencev1alpha1.ExternalProviderSpec{
			Provider: "openai",
			Endpoint: endpoint,
			Auth: inferencev1alpha1.AuthConfig{
				Type:      "apikey",
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
					Ref:         inferencev1alpha1.ExternalProviderReference{Name: providerName},
					TargetModel: targetModel,
					APIFormat:   "openai",
					Path:        "/v1/chat/completions",
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

	// 4 rules for single provider: 2 per-provider + 2 fallback
	require.Len(t, hr.Spec.Rules, 4)
	assert.Equal(t, "/"+ns+"/gpt4", *hr.Spec.Rules[0].Matches[0].Path.Value)
	assert.Equal(t, "x-ipp-selected-provider", string(hr.Spec.Rules[0].Matches[0].Headers[0].Name))

	// Backend ref points to provider's Service
	assert.Equal(t, "my-openai", string(hr.Spec.Rules[0].BackendRefs[0].Name))

	// Host header for TLS SNI
	assert.Equal(t, "api.openai.com",
		hr.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value)

	// Labels
	assert.Equal(t, managedByValue, hr.Labels[ctrlcommon.LabelManagedBy])
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
	assert.Equal(t, "gpt4", model.Status.HTTPRouteName)
}

func TestReconcile_ProviderNamespace(t *testing.T) {
	for _, scope := range []string{"omitted", "local", "foreign"} {
		t.Run(scope, func(t *testing.T) {
			ns := createTestNamespace(t)
			foreign := createTestNamespace(t)
			createExternalProvider(t, "provider", ns, "local.example.com")
			createExternalProvider(t, "provider", foreign, "foreign.example.com")
			model := newExternalModel("model", ns, "provider", "gpt")
			switch scope {
			case "local":
				model.Spec.ExternalProviderRefs[0].Ref.Namespace = ns
			case "foreign":
				model.Spec.ExternalProviderRefs[0].Ref.Namespace = foreign
			}
			require.NoError(t, k8sClient.Create(ctx, model))
			var route gatewayapiv1.HTTPRoute
			if scope == "foreign" {
				// Foreign-only references are unservable by IPP, and AGC does
				// not claim models by reference shape — in IPP namespaces it
				// never writes model status — so IPP reports the failure.
				waitForModelPhase(t, model.Name, ns, "Failed")
				require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
				cond := meta.FindStatusCondition(model.Status.Conditions, ctrlcommon.ConditionTypeReady)
				require.NotNil(t, cond)
				assert.Contains(t, cond.Message, "cross-namespace ExternalProvider reference")
				assert.Empty(t, model.Status.HTTPRouteName)
				assert.True(t, apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &route)))
			} else {
				waitForModelPhase(t, model.Name, ns, "Ready")
				require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &route))
				assert.Equal(t, "local.example.com", route.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value)
			}
		})
	}
}

func TestReconcile_ProviderNamespaceUpdate(t *testing.T) {
	for _, change := range []string{"foreign reference", "provider not ready"} {
		t.Run(change, func(t *testing.T) {
			ns := createTestNamespace(t)
			createExternalProvider(t, "provider", ns, "local.example.com")
			model := newExternalModel("model", ns, "provider", "gpt")
			require.NoError(t, k8sClient.Create(ctx, model))
			waitForModelPhase(t, model.Name, ns, "Ready")

			if change == "foreign reference" {
				require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
				old := model.DeepCopy()
				model.Spec.ExternalProviderRefs[0].Ref.Namespace = createTestNamespace(t)
				require.NoError(t, k8sClient.Patch(ctx, model, client.MergeFrom(old)))
			} else {
				provider := &inferencev1alpha1.ExternalProvider{}
				require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "provider", Namespace: ns}, provider))
				provider.Status.Phase = "Failed"
				require.NoError(t, k8sClient.Status().Update(ctx, provider))
			}

			var route gatewayapiv1.HTTPRoute
			if change == "foreign reference" {
				// The foreign-only edit is unservable: IPP withdraws its
				// route and reports Failed. AGC does not claim models by
				// reference shape (in IPP namespaces it never writes model
				// status), so freezing the last-written Ready would leave
				// the model claiming service it no longer has.
				require.Eventually(t, func() bool {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &route)
					return apierrors.IsNotFound(err)
				}, 10*time.Second, 100*time.Millisecond, "the foreign-only edit must withdraw the IPP-owned route")
				waitForModelPhase(t, model.Name, ns, "Failed")
				require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
				cond := meta.FindStatusCondition(model.Status.Conditions, ctrlcommon.ConditionTypeReady)
				require.NotNil(t, cond)
				assert.Contains(t, cond.Message, "cross-namespace ExternalProvider reference")
				assert.Empty(t, model.Status.HTTPRouteName)
			} else {
				waitForModelPhase(t, model.Name, ns, "Failed")
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &route)
				require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
				require.NoError(t, err, "transient readiness failure must keep the last-known-good route")
				assert.Equal(t, model.Name, model.Status.HTTPRouteName)
			}
		})
	}
}

func TestReconcile_AGCManagedGatewayRefs(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "provider", ns, "local.example.com")

	model := newExternalModel("agc-model", ns, "provider", "gpt")
	model.Spec.GatewayRefs = []inferencev1alpha1.NamespacedObjectReference{
		{Name: "tenant-gateway", Namespace: "gateway-ns"},
	}
	require.NoError(t, k8sClient.Create(ctx, model))

	// AGC-managed shape skipped entirely
	require.Never(t, func() bool {
		m := &inferencev1alpha1.ExternalModel{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), m); err != nil {
			return false
		}
		return m.Status.Phase != ""
	}, 2*time.Second, 100*time.Millisecond, "IPP must not write status for AGC-managed models")
	assert.True(t, apierrors.IsNotFound(
		k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &gatewayapiv1.HTTPRoute{})))
}

func TestReconcile_AGCManagedTransition(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "provider", ns, "local.example.com")

	// Start legacy-shaped: reconciled normally.
	model := newExternalModel("transition", ns, "provider", "gpt")
	require.NoError(t, k8sClient.Create(ctx, model))
	waitForModelPhase(t, "transition", ns, "Ready")
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &gatewayapiv1.HTTPRoute{}))

	// Edit to the AGC-managed shape: IPP must withdraw its own route...
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
	old := model.DeepCopy()
	model.Spec.GatewayRefs = []inferencev1alpha1.NamespacedObjectReference{
		{Name: "tenant-gateway", Namespace: "gateway-ns"},
	}
	require.NoError(t, k8sClient.Patch(ctx, model, client.MergeFrom(old)))
	require.Eventually(t, func() bool {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &gatewayapiv1.HTTPRoute{})
		return apierrors.IsNotFound(err)
	}, 10*time.Second, 100*time.Millisecond, "AGC-managed edit must withdraw the IPP-owned route")

	// ...but never touch a route it does not own. Simulate AGC (or a user)
	// re-creating the same-named route after the withdrawal.
	foreignRoute := &gatewayapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "transition",
			Namespace: ns,
			Labels:    map[string]string{ctrlcommon.LabelManagedBy: "ai-gateway-controller"},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, foreignRoute))

	// Trigger another reconcile of the AGC-managed model.
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
	old = model.DeepCopy()
	model.Annotations = map[string]string{"inference.opendatahub.io/probe": "reconcile"}
	require.NoError(t, k8sClient.Patch(ctx, model, client.MergeFrom(old)))

	// The foreign-owned route must survive and the status stay untouched.
	assert.Never(t, func() bool {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &gatewayapiv1.HTTPRoute{})
		return err != nil
	}, 2*time.Second, 100*time.Millisecond, "IPP must not delete an HTTPRoute it does not own")
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
	assert.Equal(t, "Ready", model.Status.Phase, "IPP must not rewrite status after the AGC-managed edit")
}

// foreignProviderRef returns a provider reference pointing at another
// namespace. It deliberately reuses a name that also exists locally, so a
// regression that resolves it by name in the model namespace instead of
// skipping it changes the resolved reference set.
func foreignProviderRef(name, namespace, targetModel string) inferencev1alpha1.ExternalProviderRef {
	return inferencev1alpha1.ExternalProviderRef{
		Ref:         inferencev1alpha1.ExternalProviderReference{Name: name, Namespace: namespace},
		TargetModel: targetModel,
		APIFormat:   "openai",
		Path:        "/v1/chat/completions",
	}
}

func TestReconcile_MixedProviderReferences(t *testing.T) {
	ns := createTestNamespace(t)
	foreign := createTestNamespace(t)
	createExternalProvider(t, "provider", ns, "local.example.com")
	// No provider exists in the foreign namespace or under a different
	// local name: the foreign reference must be skipped, not resolved.

	model := newExternalModel("mixed", ns, "provider", "gpt")
	model.Spec.ExternalProviderRefs = append(model.Spec.ExternalProviderRefs,
		foreignProviderRef("provider", foreign, "gpt"))
	require.NoError(t, k8sClient.Create(ctx, model))

	waitForModelPhase(t, "mixed", ns, "Ready")

	// The route is kept and backed by the local provider only: one resolved
	// reference produces 2 per-provider rules + 2 fallback rules.
	route := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), route))
	require.Len(t, route.Spec.Rules, 4, "the foreign reference must be skipped, not resolved")
	assert.Equal(t, "local.example.com", route.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value)
}

func TestReconcile_MixedReferenceHandoff(t *testing.T) {
	ns := createTestNamespace(t)
	foreign := createTestNamespace(t)
	createExternalProvider(t, "provider", ns, "local.example.com")

	// Start legacy-shaped: reconciled normally.
	model := newExternalModel("handoff", ns, "provider", "gpt")
	require.NoError(t, k8sClient.Create(ctx, model))
	waitForModelPhase(t, "handoff", ns, "Ready")
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &gatewayapiv1.HTTPRoute{}))

	// Adding a foreign reference alongside the local one must not withdraw
	// the route: the model keeps serving through its local reference until
	// it is actually handed to AGC.
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
	old := model.DeepCopy()
	model.Spec.ExternalProviderRefs = append(model.Spec.ExternalProviderRefs,
		foreignProviderRef("provider", foreign, "gpt"))
	require.NoError(t, k8sClient.Patch(ctx, model, client.MergeFrom(old)))

	require.Eventually(t, func() bool {
		m := &inferencev1alpha1.ExternalModel{}
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), m); err != nil {
			return false
		}
		cond := meta.FindStatusCondition(m.Status.Conditions, ctrlcommon.ConditionTypeReady)
		return m.Status.Phase == "Ready" && cond != nil && cond.ObservedGeneration == m.Generation
	}, 10*time.Second, 100*time.Millisecond, "the mixed edit must keep the model Ready")

	route := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), route),
		"the mixed edit must keep the serving route")
	assert.Equal(t, "local.example.com", route.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value)

	// Handing the model to AGC (gatewayRefs) withdraws the route and
	// freezes status.
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
	old = model.DeepCopy()
	model.Spec.GatewayRefs = []inferencev1alpha1.NamespacedObjectReference{
		{Name: "tenant-gateway", Namespace: "gateway-ns"},
	}
	require.NoError(t, k8sClient.Patch(ctx, model, client.MergeFrom(old)))
	require.Eventually(t, func() bool {
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &gatewayapiv1.HTTPRoute{})
		return apierrors.IsNotFound(err)
	}, 10*time.Second, 100*time.Millisecond, "the gatewayRefs edit must withdraw the IPP-owned route")
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), model))
	assert.Equal(t, "Ready", model.Status.Phase, "IPP must not rewrite status after the AGC-managed edit")
}

func TestReconcile_MixedReferenceLocalFailureKeepsRoute(t *testing.T) {
	ns := createTestNamespace(t)
	foreign := createTestNamespace(t)
	createExternalProvider(t, "provider", ns, "local.example.com")

	model := newExternalModel("mixed-blip", ns, "provider", "gpt")
	model.Spec.ExternalProviderRefs = append(model.Spec.ExternalProviderRefs,
		foreignProviderRef("provider", foreign, "gpt"))
	require.NoError(t, k8sClient.Create(ctx, model))
	waitForModelPhase(t, "mixed-blip", ns, "Ready")

	// The local provider degrades: the mixed model reports Failed but, like
	// a local-only model, keeps its last-known-good route rather than
	// withdrawing it.
	provider := &inferencev1alpha1.ExternalProvider{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "provider", Namespace: ns}, provider))
	provider.Status.Phase = "Failed"
	require.NoError(t, k8sClient.Status().Update(ctx, provider))

	waitForModelPhase(t, "mixed-blip", ns, "Failed")
	require.NoError(t, k8sClient.Get(ctx, client.ObjectKeyFromObject(model), &gatewayapiv1.HTTPRoute{}),
		"a transient local failure must keep the last-known-good route")
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

func TestReconcile_UnresolvedPathPlaceholder(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "my-vertex", ns, "us-central1-aiplatform.googleapis.com")

	model := &inferencev1alpha1.ExternalModel{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-path", Namespace: ns},
		Spec: inferencev1alpha1.ExternalModelSpec{
			ExternalProviderRefs: []inferencev1alpha1.ExternalProviderRef{
				{
					Ref:         inferencev1alpha1.ExternalProviderReference{Name: "my-vertex"},
					TargetModel: "gemini-pro",
					APIFormat:   "openai-chat",
					Path:        "/v1/projects/{project}/locations/{location}/chat/completions",
				},
			},
		},
	}
	require.NoError(t, k8sClient.Create(ctx, model))

	waitForModelPhase(t, "bad-path", ns, "Failed")

	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "bad-path", Namespace: ns}, model))
	require.Len(t, model.Status.Conditions, 1)
	assert.Equal(t, "ReconcileFailed", model.Status.Conditions[0].Reason)
	assert.Contains(t, model.Status.Conditions[0].Message, "unresolved placeholders")
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

	// Per-provider header match on rule 1 uses CR name + x-ipp-selected-provider
	assert.Equal(t, "gpt4", hr1.Spec.Rules[1].Matches[0].Headers[0].Value)
	assert.Equal(t, "gpt35", hr2.Spec.Rules[1].Matches[0].Headers[0].Value)

	// Different path prefixes
	assert.Equal(t, "/"+ns+"/gpt4", *hr1.Spec.Rules[0].Matches[0].Path.Value)
	assert.Equal(t, "/"+ns+"/gpt35", *hr2.Spec.Rules[0].Matches[0].Path.Value)

	// Independent labels and owners
	assert.Equal(t, "gpt4", hr1.Labels[labelExternalModel])
	assert.Equal(t, "gpt35", hr2.Labels[labelExternalModel])
	assert.Equal(t, "gpt4", hr1.OwnerReferences[0].Name)
	assert.Equal(t, "gpt35", hr2.OwnerReferences[0].Name)
}

func TestReconcile_CustomPortProvider(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProviderWithAnnotations(t, "vllm-provider", ns, "vllm.internal.svc", map[string]string{
		ctrlcommon.AnnotationPort: "8080",
		ctrlcommon.AnnotationTLS:  "false",
	})

	model := newExternalModel("vllm-model", ns, "vllm-provider", "my-model")
	require.NoError(t, k8sClient.Create(ctx, model))

	waitForModelPhase(t, "vllm-model", ns, "Ready")

	// Verify HTTPRoute uses custom port in backend ref
	hr := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "vllm-model", Namespace: ns}, hr))

	require.Len(t, hr.Spec.Rules, 4)
	assert.Equal(t, gatewayapiv1.PortNumber(8080), *hr.Spec.Rules[0].BackendRefs[0].Port)
}

func TestBuildHTTPRoute_CustomPort(t *testing.T) {
	refs := []resolvedRef{{
		providerName: "my-vllm", providerEndpoint: "vllm.internal.svc", targetModel: "my-model", port: 8080,
	}}
	hr := buildHTTPRoute(refs, "model1", "model1", "models",
		"default-gateway", "openshift-ingress", "300s", commonLabels("model1"))

	require.Len(t, hr.Spec.Rules, 4)
	// Per-provider rule
	assert.Equal(t, gatewayapiv1.PortNumber(8080), *hr.Spec.Rules[0].BackendRefs[0].Port)
	// Fallback rule
	assert.Equal(t, gatewayapiv1.PortNumber(8080), *hr.Spec.Rules[2].BackendRefs[0].Port)
}

// --- resource builder tests (folded from resources_test.go) ---

func TestCommonLabels(t *testing.T) {
	labels := commonLabels("gpt4")
	assert.Equal(t, managedByValue, labels[ctrlcommon.LabelManagedBy])
	assert.Equal(t, "gpt4", labels[labelExternalModel])
	assert.Len(t, labels, 2)
}

func TestBuildHTTPRoute_SingleProvider(t *testing.T) {
	refs := []resolvedRef{{
		providerName: "my-openai", providerEndpoint: "api.openai.com", targetModel: "gpt-4o", port: 443,
	}}
	hr := buildHTTPRoute(refs, "gpt4", "gpt4", "models",
		"default-gateway", "openshift-ingress", "300s", commonLabels("gpt4"))

	assert.Equal(t, "gpt4", hr.Name)
	assert.Equal(t, "models", hr.Namespace)

	// 4 rules: 2 per-provider + 2 fallback
	require.Len(t, hr.Spec.Rules, 4)

	// Rule 0: path + x-ipp-selected-provider
	assert.Equal(t, "/models/gpt4", *hr.Spec.Rules[0].Matches[0].Path.Value)
	require.Len(t, hr.Spec.Rules[0].Matches[0].Headers, 1)
	assert.Equal(t, "x-ipp-selected-provider", string(hr.Spec.Rules[0].Matches[0].Headers[0].Name))
	assert.Equal(t, "my-openai", hr.Spec.Rules[0].Matches[0].Headers[0].Value)
	assert.Equal(t, "my-openai", string(hr.Spec.Rules[0].BackendRefs[0].Name))

	// Rule 2: fallback path (no provider header)
	assert.Empty(t, hr.Spec.Rules[2].Matches[0].Headers)

	// Host filter
	assert.Equal(t, "api.openai.com", hr.Spec.Rules[0].Filters[0].RequestHeaderModifier.Set[0].Value)
}

func TestBuildHTTPRoute_MultiProvider(t *testing.T) {
	refs := []resolvedRef{
		{providerName: "anthropic", providerEndpoint: "api.anthropic.com", targetModel: "claude-opus-4-8", port: 443},
		{providerName: "sim-vertex", providerEndpoint: "sim.example.com", targetModel: "claude-opus-4-8", port: 443},
	}
	hr := buildHTTPRoute(refs, "claude", "claude", "models",
		"my-gateway", "gateway-ns", "300s", commonLabels("claude"))

	// 6 rules: 2 per-provider x 2 + 2 fallback
	require.Len(t, hr.Spec.Rules, 6)

	// Anthropic rules (0,1)
	assert.Equal(t, "anthropic", hr.Spec.Rules[0].Matches[0].Headers[0].Value)
	assert.Equal(t, "anthropic", string(hr.Spec.Rules[0].BackendRefs[0].Name))

	// Sim-vertex rules (2,3)
	assert.Equal(t, "sim-vertex", hr.Spec.Rules[2].Matches[0].Headers[0].Value)
	assert.Equal(t, "sim-vertex", string(hr.Spec.Rules[2].BackendRefs[0].Name))

	// Fallback rules (4,5) point to refs[0] (anthropic)
	assert.Empty(t, hr.Spec.Rules[4].Matches[0].Headers)
	assert.Equal(t, "anthropic", string(hr.Spec.Rules[4].BackendRefs[0].Name))
}

func TestBuildHTTPRoute_ClientModelName(t *testing.T) {
	refs := []resolvedRef{{
		providerName: "my-openai", providerEndpoint: "api.openai.com", targetModel: "gpt-4o", port: 443,
	}}
	hr := buildHTTPRoute(refs, "gpt4", "openai/gpt-4o", "models",
		"default-gateway", "openshift-ingress", "300s", commonLabels("gpt4"))

	// Route identity and per-model path prefix stay on the CR name.
	assert.Equal(t, "gpt4", hr.Name)
	assert.Equal(t, "/models/gpt4", *hr.Spec.Rules[0].Matches[0].Path.Value)

	// Header matches use the client-facing name (per-provider rule 1, fallback rule 3).
	for _, i := range []int{1, 3} {
		headers := hr.Spec.Rules[i].Matches[0].Headers
		require.NotEmpty(t, headers, "rule %d", i)
		assert.Equal(t, "X-Gateway-Model-Name", string(headers[0].Name), "rule %d", i)
		assert.Equal(t, "openai/gpt-4o", headers[0].Value, "rule %d", i)
	}
}

func TestReconcile_SpecModelName(t *testing.T) {
	ns := createTestNamespace(t)
	createExternalProvider(t, "my-openai", ns, "api.openai.com")

	model := newExternalModel("gpt4", ns, "my-openai", "gpt-4o")
	model.Spec.ModelName = "openai/gpt-4o"
	require.NoError(t, k8sClient.Create(ctx, model))

	waitForModelPhase(t, "gpt4", ns, "Ready")

	hr := &gatewayapiv1.HTTPRoute{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "gpt4", Namespace: ns}, hr))
	require.Len(t, hr.Spec.Rules, 4)
	assert.Equal(t, "openai/gpt-4o", hr.Spec.Rules[1].Matches[0].Headers[0].Value)
	assert.Equal(t, "openai/gpt-4o", hr.Spec.Rules[3].Matches[0].Headers[0].Value)
}
