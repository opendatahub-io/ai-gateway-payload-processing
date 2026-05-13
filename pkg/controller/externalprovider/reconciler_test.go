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
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	ctrlcommon "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/common"
)

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc

	serviceEntryGVK = schema.GroupVersionKind{
		Group: "networking.istio.io", Version: "v1", Kind: "ServiceEntry",
	}
	destinationRuleGVK = schema.GroupVersionKind{
		Group: "networking.istio.io", Version: "v1", Kind: "DestinationRule",
	}
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
			filepath.Join("testdata", "istio-crds"),
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
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
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
		ObjectMeta: metav1.ObjectMeta{GenerateName: "test-ep-"},
	}
	require.NoError(t, k8sClient.Create(ctx, ns))
	t.Cleanup(func() {
		_ = k8sClient.Delete(ctx, ns)
	})
	return ns.Name
}

func createSecret(t *testing.T, name, namespace string) {
	t.Helper()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       map[string][]byte{"api-key": []byte("test-key")},
	}
	require.NoError(t, k8sClient.Create(ctx, secret))
}

func newExternalProvider(name, namespace, endpoint, secretName string) *inferencev1alpha1.ExternalProvider {
	return &inferencev1alpha1.ExternalProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: inferencev1alpha1.ExternalProviderSpec{
			Provider: "openai",
			Endpoint: endpoint,
			Auth: inferencev1alpha1.AuthConfig{
				SecretRef: inferencev1alpha1.NameReference{Name: secretName},
			},
		},
	}
}

func waitForPhase(t *testing.T, name, namespace, expectedPhase string) {
	t.Helper()
	require.Eventually(t, func() bool {
		p := &inferencev1alpha1.ExternalProvider{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, p); err != nil {
			return false
		}
		return p.Status.Phase == expectedPhase
	}, 10*time.Second, 100*time.Millisecond, "expected phase %s", expectedPhase)
}

func getUnstructured(t *testing.T, gvk schema.GroupVersionKind, name, namespace string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, obj))
	return obj
}

// --- test cases ---

func TestReconcile_CreatesResources(t *testing.T) {
	ns := createTestNamespace(t)
	createSecret(t, "openai-key", ns)

	provider := newExternalProvider("my-openai", ns, "api.openai.com", "openai-key")
	require.NoError(t, k8sClient.Create(ctx, provider))

	waitForPhase(t, "my-openai", ns, "Ready")

	// Verify Service
	svc := &corev1.Service{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "my-openai", Namespace: ns}, svc))
	assert.Equal(t, corev1.ServiceTypeExternalName, svc.Spec.Type)
	assert.Equal(t, "api.openai.com", svc.Spec.ExternalName)
	assert.Equal(t, int32(443), svc.Spec.Ports[0].Port)
	assert.Equal(t, managedByValue, svc.Labels[ctrlcommon.LabelManagedBy])

	// Verify ServiceEntry
	se := getUnstructured(t, serviceEntryGVK, "my-openai", ns)
	seSpec := se.Object["spec"].(map[string]any)
	hosts := seSpec["hosts"].([]any)
	assert.Equal(t, "api.openai.com", hosts[0])
	assert.Equal(t, "MESH_EXTERNAL", seSpec["location"])
	assert.Equal(t, "DNS", seSpec["resolution"])

	// Verify DestinationRule
	dr := getUnstructured(t, destinationRuleGVK, "my-openai", ns)
	drSpec := dr.Object["spec"].(map[string]any)
	assert.Equal(t, "api.openai.com", drSpec["host"])
	tp := drSpec["trafficPolicy"].(map[string]any)
	tlsCfg := tp["tls"].(map[string]any)
	assert.Equal(t, "SIMPLE", tlsCfg["mode"])

	// Verify status condition
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "my-openai", Namespace: ns}, provider))
	require.Len(t, provider.Status.Conditions, 1)
	assert.Equal(t, ctrlcommon.ConditionTypeReady, provider.Status.Conditions[0].Type)
	assert.Equal(t, metav1.ConditionTrue, provider.Status.Conditions[0].Status)
	assert.Equal(t, "Reconciled", provider.Status.Conditions[0].Reason)
}

func TestReconcile_UpdateEndpoint(t *testing.T) {
	ns := createTestNamespace(t)
	createSecret(t, "key", ns)

	provider := newExternalProvider("updatable", ns, "api.openai.com", "key")
	require.NoError(t, k8sClient.Create(ctx, provider))
	waitForPhase(t, "updatable", ns, "Ready")

	// Update endpoint
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "updatable", Namespace: ns}, provider))
	provider.Spec.Endpoint = "api.anthropic.com"
	require.NoError(t, k8sClient.Update(ctx, provider))

	// Wait for Service to reflect the new endpoint
	require.Eventually(t, func() bool {
		svc := &corev1.Service{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "updatable", Namespace: ns}, svc); err != nil {
			return false
		}
		return svc.Spec.ExternalName == "api.anthropic.com"
	}, 10*time.Second, 100*time.Millisecond)

	// Verify ServiceEntry updated
	require.Eventually(t, func() bool {
		se := getUnstructured(t, serviceEntryGVK, "updatable", ns)
		seSpec, ok := se.Object["spec"].(map[string]any)
		if !ok {
			return false
		}
		hosts, ok := seSpec["hosts"].([]any)
		return ok && len(hosts) > 0 && hosts[0] == "api.anthropic.com"
	}, 10*time.Second, 100*time.Millisecond)

	// Verify DestinationRule updated
	require.Eventually(t, func() bool {
		dr := getUnstructured(t, destinationRuleGVK, "updatable", ns)
		drSpec, ok := dr.Object["spec"].(map[string]any)
		return ok && drSpec["host"] == "api.anthropic.com"
	}, 10*time.Second, 100*time.Millisecond)
}

// --- resource builder tests (folded from resources_test.go) ---

func TestCommonLabels(t *testing.T) {
	labels := commonLabels("my-openai")

	assert.Equal(t, managedByValue, labels[ctrlcommon.LabelManagedBy])
	assert.Equal(t, "my-openai", labels[labelExternalProvider])
	assert.Len(t, labels, 2)
}

func TestBuildService(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		svcName   string
		namespace string
		port      int32
	}{
		{
			name:      "standard OpenAI provider",
			endpoint:  "api.openai.com",
			svcName:   "my-openai",
			namespace: "models",
			port:      443,
		},
		{
			name:      "custom port provider",
			endpoint:  "bedrock.us-east-1.amazonaws.com",
			svcName:   "my-bedrock",
			namespace: "llm",
			port:      8443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := buildService(tt.endpoint, tt.svcName, tt.namespace, tt.port, commonLabels(tt.svcName))

			assert.Equal(t, tt.svcName, svc.Name)
			assert.Equal(t, tt.namespace, svc.Namespace)
			assert.Equal(t, corev1.ServiceTypeExternalName, svc.Spec.Type)
			assert.Equal(t, tt.endpoint, svc.Spec.ExternalName)
			require.Len(t, svc.Spec.Ports, 1)
			assert.Equal(t, tt.port, svc.Spec.Ports[0].Port)
			assert.Equal(t, tt.port, svc.Spec.Ports[0].TargetPort.IntVal)
			assert.Equal(t, managedByValue, svc.Labels[ctrlcommon.LabelManagedBy])
			assert.Equal(t, tt.svcName, svc.Labels[labelExternalProvider])
		})
	}
}

func TestBuildServiceEntry(t *testing.T) {
	se := buildServiceEntry("api.openai.com", "my-openai", "models", 443, commonLabels("my-openai"))

	assert.Equal(t, "ServiceEntry", se.GetKind())
	assert.Equal(t, "networking.istio.io/v1", se.GetAPIVersion())
	assert.Equal(t, "my-openai", se.GetName())
	assert.Equal(t, "models", se.GetNamespace())
	assert.Equal(t, managedByValue, se.GetLabels()[ctrlcommon.LabelManagedBy])

	spec, ok := se.Object["spec"].(map[string]any)
	require.True(t, ok)

	hosts, ok := spec["hosts"].([]any)
	require.True(t, ok)
	assert.Equal(t, "api.openai.com", hosts[0])

	assert.Equal(t, "MESH_EXTERNAL", spec["location"])
	assert.Equal(t, "DNS", spec["resolution"])

	ports, ok := spec["ports"].([]any)
	require.True(t, ok)
	require.Len(t, ports, 1)
	port, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(443), port["number"])
	assert.Equal(t, "https", port["name"])
	assert.Equal(t, "HTTPS", port["protocol"])
}

func TestBuildServiceEntry_CustomPort(t *testing.T) {
	se := buildServiceEntry("vllm.internal.svc", "my-vllm", "models", 8443, commonLabels("my-vllm"))

	spec, ok := se.Object["spec"].(map[string]any)
	require.True(t, ok)
	ports, ok := spec["ports"].([]any)
	require.True(t, ok)
	port, ok := ports[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, int64(8443), port["number"])
}

func TestBuildDestinationRule(t *testing.T) {
	dr := buildDestinationRule("api.openai.com", "my-openai", "models", commonLabels("my-openai"))

	assert.Equal(t, "DestinationRule", dr.GetKind())
	assert.Equal(t, "networking.istio.io/v1", dr.GetAPIVersion())
	assert.Equal(t, "my-openai", dr.GetName())
	assert.Equal(t, "models", dr.GetNamespace())
	assert.Equal(t, managedByValue, dr.GetLabels()[ctrlcommon.LabelManagedBy])

	spec, ok := dr.Object["spec"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "api.openai.com", spec["host"])

	trafficPolicy, ok := spec["trafficPolicy"].(map[string]any)
	require.True(t, ok)
	tls, ok := trafficPolicy["tls"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "SIMPLE", tls["mode"])
}

func TestBuildDestinationRule_DifferentProviders(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		provider string
	}{
		{"anthropic", "api.anthropic.com", "my-anthropic"},
		{"bedrock", "bedrock.us-east-1.amazonaws.com", "my-bedrock"},
		{"azure", "my-deployment.openai.azure.com", "my-azure"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dr := buildDestinationRule(tt.endpoint, tt.provider, "models", commonLabels(tt.provider))

			spec, ok := dr.Object["spec"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, tt.endpoint, spec["host"])
			assert.Equal(t, tt.provider, dr.GetName())
		})
	}
}

func TestReconcile_DeleteProvider(t *testing.T) {
	ns := createTestNamespace(t)
	createSecret(t, "key", ns)

	provider := newExternalProvider("to-delete", ns, "api.openai.com", "key")
	require.NoError(t, k8sClient.Create(ctx, provider))
	waitForPhase(t, "to-delete", ns, "Ready")

	// Verify OwnerReferences on Service
	svc := &corev1.Service{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "to-delete", Namespace: ns}, svc))
	require.Len(t, svc.OwnerReferences, 1)
	assert.Equal(t, "ExternalProvider", svc.OwnerReferences[0].Kind)
	assert.Equal(t, "to-delete", svc.OwnerReferences[0].Name)
	assert.True(t, *svc.OwnerReferences[0].Controller)

	// Verify OwnerReferences on ServiceEntry
	se := getUnstructured(t, serviceEntryGVK, "to-delete", ns)
	seOwners := se.GetOwnerReferences()
	require.Len(t, seOwners, 1)
	assert.Equal(t, "ExternalProvider", seOwners[0].Kind)

	// Verify OwnerReferences on DestinationRule
	dr := getUnstructured(t, destinationRuleGVK, "to-delete", ns)
	drOwners := dr.GetOwnerReferences()
	require.Len(t, drOwners, 1)
	assert.Equal(t, "ExternalProvider", drOwners[0].Kind)

	// Delete the provider
	require.NoError(t, k8sClient.Delete(ctx, provider))

	// Verify the provider is gone
	require.Eventually(t, func() bool {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: "to-delete", Namespace: ns}, &inferencev1alpha1.ExternalProvider{})
		return apierrors.IsNotFound(err)
	}, 10*time.Second, 100*time.Millisecond)
}

func TestReconcile_Idempotency(t *testing.T) {
	ns := createTestNamespace(t)
	createSecret(t, "key", ns)

	provider := newExternalProvider("idempotent", ns, "api.openai.com", "key")
	require.NoError(t, k8sClient.Create(ctx, provider))
	waitForPhase(t, "idempotent", ns, "Ready")

	// Record resourceVersions of all managed resources
	svc := &corev1.Service{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "idempotent", Namespace: ns}, svc))
	svcRV := svc.ResourceVersion

	se := getUnstructured(t, serviceEntryGVK, "idempotent", ns)
	seRV := se.GetResourceVersion()

	dr := getUnstructured(t, destinationRuleGVK, "idempotent", ns)
	drRV := dr.GetResourceVersion()

	// Trigger a re-reconcile by adding an annotation (bumps Generation)
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "idempotent", Namespace: ns}, provider))
	if provider.Annotations == nil {
		provider.Annotations = map[string]string{}
	}
	provider.Annotations["test"] = "trigger-reconcile"
	require.NoError(t, k8sClient.Update(ctx, provider))

	// Wait for the reconciler to process the new generation
	require.Eventually(t, func() bool {
		p := &inferencev1alpha1.ExternalProvider{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "idempotent", Namespace: ns}, p); err != nil {
			return false
		}
		if len(p.Status.Conditions) == 0 {
			return false
		}
		return p.Status.Conditions[0].ObservedGeneration == p.Generation
	}, 10*time.Second, 100*time.Millisecond)

	// Verify resourceVersions unchanged — no unnecessary writes
	svc = &corev1.Service{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "idempotent", Namespace: ns}, svc))
	assert.Equal(t, svcRV, svc.ResourceVersion, "Service should not have been updated")

	se = getUnstructured(t, serviceEntryGVK, "idempotent", ns)
	assert.Equal(t, seRV, se.GetResourceVersion(), "ServiceEntry should not have been updated")

	dr = getUnstructured(t, destinationRuleGVK, "idempotent", ns)
	assert.Equal(t, drRV, dr.GetResourceVersion(), "DestinationRule should not have been updated")
}

func TestReconcile_MissingSecret(t *testing.T) {
	ns := createTestNamespace(t)
	// Intentionally do NOT create the secret

	provider := newExternalProvider("no-secret", ns, "api.openai.com", "nonexistent-secret")
	require.NoError(t, k8sClient.Create(ctx, provider))

	waitForPhase(t, "no-secret", ns, "Failed")

	// Verify condition has meaningful message
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "no-secret", Namespace: ns}, provider))
	require.Len(t, provider.Status.Conditions, 1)
	assert.Equal(t, metav1.ConditionFalse, provider.Status.Conditions[0].Status)
	assert.Equal(t, "SecretNotFound", provider.Status.Conditions[0].Reason)
	assert.Contains(t, provider.Status.Conditions[0].Message, "nonexistent-secret")

	// Networking resources should NOT be created (validation runs before resource creation)
	svc := &corev1.Service{}
	err := k8sClient.Get(ctx, types.NamespacedName{Name: "no-secret", Namespace: ns}, svc)
	assert.True(t, apierrors.IsNotFound(err), "Service should not exist when secret is missing")
}

func TestReconcile_TwoProviders(t *testing.T) {
	ns := createTestNamespace(t)
	createSecret(t, "openai-key", ns)
	createSecret(t, "anthropic-key", ns)

	p1 := newExternalProvider("prov-openai", ns, "api.openai.com", "openai-key")
	p2 := newExternalProvider("prov-anthropic", ns, "api.anthropic.com", "anthropic-key")
	p2.Spec.Provider = "anthropic"
	require.NoError(t, k8sClient.Create(ctx, p1))
	require.NoError(t, k8sClient.Create(ctx, p2))

	waitForPhase(t, "prov-openai", ns, "Ready")
	waitForPhase(t, "prov-anthropic", ns, "Ready")

	// Verify independent Services
	svc1 := &corev1.Service{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "prov-openai", Namespace: ns}, svc1))
	assert.Equal(t, "api.openai.com", svc1.Spec.ExternalName)

	svc2 := &corev1.Service{}
	require.NoError(t, k8sClient.Get(ctx, types.NamespacedName{Name: "prov-anthropic", Namespace: ns}, svc2))
	assert.Equal(t, "api.anthropic.com", svc2.Spec.ExternalName)

	// Verify independent ServiceEntries
	se1 := getUnstructured(t, serviceEntryGVK, "prov-openai", ns)
	se2 := getUnstructured(t, serviceEntryGVK, "prov-anthropic", ns)

	spec1 := se1.Object["spec"].(map[string]any)
	spec2 := se2.Object["spec"].(map[string]any)
	assert.Equal(t, "api.openai.com", spec1["hosts"].([]any)[0])
	assert.Equal(t, "api.anthropic.com", spec2["hosts"].([]any)[0])

	// Verify labels don't cross-contaminate
	assert.Equal(t, "prov-openai", se1.GetLabels()[labelExternalProvider])
	assert.Equal(t, "prov-anthropic", se2.GetLabels()[labelExternalProvider])

	// Verify OwnerReferences point to correct provider
	assert.Equal(t, "prov-openai", se1.GetOwnerReferences()[0].Name)
	assert.Equal(t, "prov-anthropic", se2.GetOwnerReferences()[0].Name)
}
