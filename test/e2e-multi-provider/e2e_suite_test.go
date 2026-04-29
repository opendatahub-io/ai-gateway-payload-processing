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

package e2e_multi_provider

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/scheme"
)

const (
	defaultNs                = "e2e-multi-provider"
	defaultSimulatorEndpoint = "3-13-21-181.sslip.io"
	defaultGatewayName       = "maas-default-gateway"
	defaultGatewayNamespace  = "istio-system"
)

var (
	kubeClient  kubernetes.Interface
	nsName      string
	gatewayNs   string
	gatewayName string
	simulatorEP string
	curlTimeout = 30 * time.Second
)

func TestE2E(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Multi-Provider Traffic Splitting E2E Suite")
}

var _ = ginkgo.BeforeSuite(func() {
	nsName = envOr("E2E_NS", defaultNs)
	gatewayNs = envOr("E2E_GATEWAY_NAMESPACE", defaultGatewayNamespace)
	gatewayName = envOr("E2E_GATEWAY_NAME", defaultGatewayName)
	simulatorEP = envOr("E2E_SIMULATOR_ENDPOINT", defaultSimulatorEndpoint)

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), nil,
	).ClientConfig()
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	kubeClient, err = kubernetes.NewForConfig(config)
	gomega.Expect(err).NotTo(gomega.HaveOccurred())

	setupInfra()
})

var _ = ginkgo.AfterSuite(func() {
	cleanupInfra()
})

func setupInfra() {
	ginkgo.By("Creating test namespace")
	createNamespace(nsName)

	ginkgo.By("Creating curl client pod")
	kubectlApplyLiteral(fmt.Sprintf(`
apiVersion: v1
kind: Pod
metadata:
  name: curl
  namespace: %s
spec:
  containers:
  - name: curl
    image: curlimages/curl:7.83.1
    command: ["tail", "-f", "/dev/null"]
`, nsName))
	waitForPodReady("curl", nsName)

	ginkgo.By("Creating provider resources")
	createProviderResources()

	ginkgo.By("Waiting for controller to create networking resources")
	waitForControllerResources()
}

func cleanupInfra() {
	kubectlDeleteResource("externalmodel.inference.opendatahub.io", "multi-split", nsName)
	kubectlDeleteResource("externalprovider.inference.opendatahub.io", "mp-openai", nsName)
	kubectlDeleteResource("externalprovider.inference.opendatahub.io", "mp-anthropic", nsName)
	kubectlDeleteResource("secret", "mp-openai-key", nsName)
	kubectlDeleteResource("secret", "mp-anthropic-key", nsName)
	kubectlDeleteResource("pod", "curl", nsName)
	_ = kubeClient.CoreV1().Namespaces().Delete(context.TODO(), nsName, metav1.DeleteOptions{})
}

func createProviderResources() {
	kubectlApplyLiteral(fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: mp-openai-key
  namespace: %s
  labels:
    inference.networking.k8s.io/bbr-managed: "true"
type: Opaque
stringData:
  api-key: llm-katan-openai-key
---
apiVersion: v1
kind: Secret
metadata:
  name: mp-anthropic-key
  namespace: %s
  labels:
    inference.networking.k8s.io/bbr-managed: "true"
type: Opaque
stringData:
  api-key: llm-katan-anthropic-key
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: mp-openai
  namespace: %s
spec:
  provider: openai
  endpoint: %s
  auth:
    secretRef:
      name: mp-openai-key
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: mp-anthropic
  namespace: %s
spec:
  provider: anthropic
  endpoint: %s
  auth:
    secretRef:
      name: mp-anthropic-key
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: multi-split
  namespace: %s
spec:
  externalProviderRefs:
    - ref:
        name: mp-openai
      targetModel: llm-katan-echo
      apiFormat: openai
      weight: 80
    - ref:
        name: mp-anthropic
      targetModel: llm-katan-echo
      apiFormat: anthropic
      weight: 20
`, nsName, nsName, nsName, simulatorEP, nsName, simulatorEP, nsName))
}

func waitForControllerResources() {
	gomega.Eventually(func() bool {
		cmd := exec.Command("kubectl", "get", "httproute", "multi-split",
			"-n", nsName, "--no-headers", "--ignore-not-found")
		out, err := cmd.Output()
		return err == nil && len(strings.TrimSpace(string(out))) > 0
	}, 60*time.Second, 2*time.Second).Should(gomega.BeTrue(),
		"HTTPRoute multi-split not created by controller within timeout")
}

// --- Kubernetes helpers ---

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func createNamespace(name string) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	_, err := kubeClient.CoreV1().Namespaces().Create(context.TODO(), ns, metav1.CreateOptions{})
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
	}
}

func kubectlApplyLiteral(yamlContent string) {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(yamlContent)
	out, err := cmd.CombinedOutput()
	if err != nil {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "kubectl apply failed: %s\n%s\n", err, string(out))
	}
	gomega.Expect(err).NotTo(gomega.HaveOccurred(), string(out))
}

func kubectlDeleteResource(kind, name, namespace string) {
	cmd := exec.Command("kubectl", "delete", kind, name, "-n", namespace, "--ignore-not-found", "--timeout=30s")
	_, _ = cmd.CombinedOutput()
}

func waitForPodReady(name, namespace string) {
	gomega.Eventually(func() bool {
		pod, err := kubeClient.CoreV1().Pods(namespace).Get(context.TODO(), name, metav1.GetOptions{})
		if err != nil {
			return false
		}
		for _, cond := range pod.Status.Conditions {
			if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
				return true
			}
		}
		return false
	}, 3*time.Minute, 250*time.Millisecond).Should(gomega.BeTrue(),
		fmt.Sprintf("Pod %s/%s not ready", namespace, name))
}

func execInPod(podName, namespace, container string, cmd []string) (string, error) {
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(), nil,
	).ClientConfig()
	if err != nil {
		return "", err
	}

	req := kubeClient.CoreV1().RESTClient().
		Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: container,
			Command:   cmd,
			Stdin:     false,
			Stdout:    true,
			Stderr:    true,
			TTY:       false,
		}, scheme.ParameterCodec)

	executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", err
	}

	var stdout, stderr strings.Builder
	err = executor.StreamWithContext(context.TODO(), remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}
