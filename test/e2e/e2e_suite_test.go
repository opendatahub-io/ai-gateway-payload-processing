package e2e

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
	defaultNs                = "bbr-e2e"
	defaultSimulatorEndpoint = "3.13.21.181"
	defaultGatewayName       = "e2e-gateway"
	defaultGatewayNamespace  = "default"
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
	ginkgo.RunSpecs(t, "BBR Plugin Chain E2E Suite")
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

	ginkgo.By("Creating simulator ServiceEntry and DestinationRule")
	kubectlApplyLiteral(fmt.Sprintf(`
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: e2e-simulator
  namespace: %s
spec:
  hosts:
  - e2e-simulator.external
  location: MESH_EXTERNAL
  ports:
  - number: 443
    name: https
    protocol: HTTPS
  resolution: STATIC
  endpoints:
  - address: %s
---
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: e2e-simulator
  namespace: %s
spec:
  host: e2e-simulator.external
  trafficPolicy:
    tls:
      mode: SIMPLE
      insecureSkipVerify: true
`, nsName, simulatorEP, nsName))

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
	for _, p := range providers {
		createProviderResources(p)
	}

	ginkgo.By("Waiting for plugin reconcilers to sync")
	time.Sleep(10 * time.Second)
}

func cleanupInfra() {
	for _, p := range providers {
		deleteProviderResources(p)
	}
	kubectlDeleteResource("destinationrule", "e2e-simulator", nsName)
	kubectlDeleteResource("serviceentry", "e2e-simulator", nsName)
	kubectlDeleteResource("pod", "curl", nsName)
	_ = kubeClient.CoreV1().Namespaces().Delete(context.TODO(), nsName, metav1.DeleteOptions{})
}

// ─── Kubernetes helpers ─────────────────────────────────────────────────────

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

// execInPod executes a command inside a pod using the Kubernetes exec API.
// This is the same pattern used by the upstream gateway-api-inference-extension E2E tests.
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
