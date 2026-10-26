// Package crosscluster holds the cross-cluster routing E2E (issue #344).
//
// It needs two Kind clusters prepared by test/e2e/scripts/setup-kind-cross-cluster.sh
// and the environment it writes to bin/cross-cluster.env. Cluster A exposes
// models hosted behind cluster B's gateway as ExternalModels of a remote-maas
// ExternalProvider; cluster B forwards to an echo backend that reports what
// it received, so assertions are made on the request that actually arrived.
package crosscluster

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

const (
	nsA = "cross-cluster-a"
	nsB = "cross-cluster-b"

	// Names used by the tests. Cluster B models set spec.modelName different
	// from metadata.name so the HTTPRoute header match on the client-facing
	// name is exercised end to end.
	modelRemoteEcho = "remote-echo"    // on A, client-facing; forwards to B
	modelEchoOnB    = "cluster-b/echo" // on B, spec.modelName of CR "echo-model"
	modelEchoTarget = "echo-upstream"  // what the echo backend must receive
	modelLoopOrigin = "loop-origin"    // on A; B maps it back to A
	modelLoopOnB    = "loop"           // on B, spec.modelName of CR "loop-model"

	keyClusterB    = "cluster-b-key"
	keyClusterA    = "cluster-a-key"
	keyEchoBackend = "echo-backend-key"
)

var (
	ctxA, ctxB                 string
	clusterNameA, clusterNameB string
	gatewayAURL                string
	gatewayAHost, gatewayBHost string
	gatewayAPort, gatewayBPort string
	echoHost, echoPort         string
)

func TestCrossCluster(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Cross-Cluster Routing")
}

var _ = ginkgo.BeforeSuite(func() {
	ctxA = mustEnv("E2E_CC_CONTEXT_A")
	ctxB = mustEnv("E2E_CC_CONTEXT_B")
	clusterNameA = mustEnv("E2E_CC_CLUSTER_NAME_A")
	clusterNameB = mustEnv("E2E_CC_CLUSTER_NAME_B")
	gatewayAURL = mustEnv("E2E_CC_GATEWAY_A_URL")
	gatewayAHost = mustEnv("E2E_CC_GATEWAY_A_HOST")
	gatewayAPort = mustEnv("E2E_CC_GATEWAY_A_HTTPS_PORT")
	gatewayBHost = mustEnv("E2E_CC_GATEWAY_B_HOST")
	gatewayBPort = mustEnv("E2E_CC_GATEWAY_B_HTTPS_PORT")
	echoHost = mustEnv("E2E_CC_ECHO_HOST")
	echoPort = mustEnv("E2E_CC_ECHO_PORT")

	ginkgo.By("Creating cluster B resources: echo provider and loop-back provider")
	kubectlApply(ctxB, fmt.Sprintf(`
apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
`, nsB))
	kubectlApply(ctxB, secretYAML("echo-backend", nsB, keyEchoBackend))
	kubectlApply(ctxB, providerYAML("echo", nsB, "openai", echoHost, echoPort, "echo-backend"))
	kubectlApply(ctxB, modelYAML("echo-model", nsB, modelEchoOnB, "echo", modelEchoTarget))
	kubectlApply(ctxB, secretYAML("cluster-a", nsB, keyClusterA))
	kubectlApply(ctxB, providerYAML("cluster-a", nsB, "remote-maas", gatewayAHost, gatewayAPort, "cluster-a"))
	kubectlApply(ctxB, modelYAML("loop-model", nsB, modelLoopOnB, "cluster-a", modelLoopOrigin))

	ginkgo.By("Creating cluster A resources: remote-maas provider for cluster B")
	kubectlApply(ctxA, fmt.Sprintf(`
apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
`, nsA))
	kubectlApply(ctxA, secretYAML("cluster-b", nsA, keyClusterB))
	kubectlApply(ctxA, providerYAML("cluster-b", nsA, "remote-maas", gatewayBHost, gatewayBPort, "cluster-b"))
	kubectlApply(ctxA, modelYAML(modelRemoteEcho, nsA, "", "cluster-b", modelEchoOnB))
	kubectlApply(ctxA, modelYAML(modelLoopOrigin, nsA, "", "cluster-b", modelLoopOnB))

	ginkgo.By("Waiting for controllers to create HTTPRoutes")
	waitForHTTPRoute(ctxB, nsB, "echo-model")
	waitForHTTPRoute(ctxB, nsB, "loop-model")
	waitForHTTPRoute(ctxA, nsA, modelRemoteEcho)
	waitForHTTPRoute(ctxA, nsA, modelLoopOrigin)
})

var _ = ginkgo.AfterSuite(func() {
	_ = kubectl(ctxA, "", "delete", "namespace", nsA, "--ignore-not-found", "--timeout=60s")
	_ = kubectl(ctxB, "", "delete", "namespace", nsB, "--ignore-not-found", "--timeout=60s")
})

// ─── Manifests ──────────────────────────────────────────────────────────────

func secretYAML(name, ns, apiKey string) string {
	return fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: %s
`, name, ns, apiKey)
}

func providerYAML(name, ns, provider, endpoint, port, secret string) string {
	return fmt.Sprintf(`
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: %s
  namespace: %s
  annotations:
    inference.opendatahub.io/port: "%s"
spec:
  provider: %s
  endpoint: %s
  auth:
    type: apikey
    secretRef:
      name: %s
`, name, ns, port, provider, endpoint, secret)
}

// modelYAML renders an ExternalModel; modelName may be empty to default to the CR name.
func modelYAML(name, ns, modelName, provider, targetModel string) string {
	spec := ""
	if modelName != "" {
		spec = "  modelName: " + modelName + "\n"
	}
	return fmt.Sprintf(`
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: %s
  namespace: %s
spec:
%s  externalProviderRefs:
    - ref:
        name: %s
      targetModel: %s
      apiFormat: openai-chat
      path: /v1/chat/completions
`, name, ns, spec, provider, targetModel)
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func mustEnv(key string) string {
	v := os.Getenv(key)
	gomega.Expect(v).NotTo(gomega.BeEmpty(),
		"%s is not set; run test/e2e/scripts/setup-kind-cross-cluster.sh and source bin/cross-cluster.env", key)
	return v
}

func kubectl(context, stdin string, args ...string) error {
	cmd := exec.Command("kubectl", append([]string{"--context", context}, args...)...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl --context %s %s: %w\n%s", context, strings.Join(args, " "), err, out)
	}
	return nil
}

func kubectlOutput(context string, args ...string) (string, error) {
	cmd := exec.Command("kubectl", append([]string{"--context", context}, args...)...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func kubectlApply(context, manifest string) {
	gomega.Expect(kubectl(context, manifest, "apply", "-f", "-")).To(gomega.Succeed())
}

func waitForHTTPRoute(context, ns, name string) {
	gomega.Eventually(func() error {
		return kubectl(context, "", "get", "httproute", name, "-n", ns)
	}, 60*time.Second, 2*time.Second).Should(gomega.Succeed(),
		"HTTPRoute %s/%s not created on %s", ns, name, context)
}
