#!/bin/bash
# E2E testing orchestrator for AI Gateway Payload Processing.
#
# Primary CI orchestrator — designed to run on Konflux against a real
# OpenShift cluster, but also works on Kind for local validation.
#
# Assumes:
#   - Cluster is available with kubectl access (OpenShift or Kind)
#   - Istio + Gateway API CRDs are installed (by DevTestOps / infra setup)
#   - External simulator endpoint is reachable (llm-katan)
#
# This script:
#   1. Validates cluster connectivity and prerequisites
#   2. Installs required CRDs (ExternalModel)
#   3. Creates a Gateway for routing
#   4. Deploys the payload processing service via Helm
#   5. Runs E2E tests using the pre-built test container image
#   6. Collects JUnit results and reports pass/fail
#
# Environment variables:
#   E2E_SIMULATOR_ENDPOINT       - Simulator IP or FQDN (default: 3.147.232.199)
#   PAYLOAD_PROCESSING_IMAGE     - BBR image to deploy (default: quay.io/opendatahub/odh-ai-gateway-payload-processing:odh-stable)
#   PAYLOAD_PROCESSING_E2E_IMAGE - Pre-built E2E test image (default: quay.io/opendatahub/ai-gateway-payload-processing-e2e:odh-stable)
#   E2E_GATEWAY_NAMESPACE        - Gateway namespace (default: istio-system)
#   E2E_GATEWAY_NAME             - Gateway name (default: e2e-gateway)
#   E2E_NS                       - Test namespace for model resources (default: e2e-models)
#   E2E_LABEL_FILTER             - Ginkgo label filter (default: runs all)
#   E2E_JUNIT_REPORT             - JUnit report path on host (default: results/e2e-junit.xml)
#   E2E_CLEANUP                  - Clean up after tests (default: true)
#   E2E_TEST_TIMEOUT             - Test timeout (default: 10m)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

E2E_SIMULATOR_ENDPOINT="${E2E_SIMULATOR_ENDPOINT:-3.147.232.199}"
ISTIO_VERSION="${ISTIO_VERSION:-1.29.2}"
PAYLOAD_PROCESSING_IMAGE="${PAYLOAD_PROCESSING_IMAGE:-quay.io/opendatahub/odh-ai-gateway-payload-processing:odh-stable}"
PAYLOAD_PROCESSING_E2E_IMAGE="${PAYLOAD_PROCESSING_E2E_IMAGE:-quay.io/opendatahub/ai-gateway-payload-processing-e2e:odh-stable}"
GATEWAY_NAMESPACE="${E2E_GATEWAY_NAMESPACE:-istio-system}"
GATEWAY_NAME="${E2E_GATEWAY_NAME:-e2e-gateway}"
E2E_NS="${E2E_NS:-e2e-models}"
E2E_LABEL_FILTER="${E2E_LABEL_FILTER:-}"
E2E_JUNIT_REPORT="${E2E_JUNIT_REPORT:-results/e2e-junit.xml}"
E2E_CLEANUP="${E2E_CLEANUP:-true}"
E2E_TEST_TIMEOUT="${E2E_TEST_TIMEOUT:-10m}"

HELM_RELEASE="payload-processing-e2e"
E2E_JOB_NAME="e2e-test-runner"

echo "================================================"
echo "Payload Processing E2E Testing"
echo "================================================"
echo "  Simulator:        $E2E_SIMULATOR_ENDPOINT"
echo "  BBR Image:        $PAYLOAD_PROCESSING_IMAGE"
echo "  E2E Image:        $PAYLOAD_PROCESSING_E2E_IMAGE"
echo "  Gateway:          $GATEWAY_NAME ($GATEWAY_NAMESPACE)"
echo "  Test Namespace:   $E2E_NS"
echo "  Label Filter:     ${E2E_LABEL_FILTER:-<all>}"
echo "  Test Timeout:     $E2E_TEST_TIMEOUT"
echo "  JUnit Report:     $E2E_JUNIT_REPORT"
echo "================================================"

cleanup() {
    if [[ "$E2E_CLEANUP" != "true" ]]; then
        echo "Skipping cleanup (E2E_CLEANUP=$E2E_CLEANUP)"
        return
    fi
    echo ""
    echo "=== Cleanup ==="
    kubectl delete job "$E2E_JOB_NAME" -n "$E2E_NS" --ignore-not-found 2>/dev/null || true
    helm uninstall "$HELM_RELEASE" -n "$GATEWAY_NAMESPACE" 2>/dev/null || true
    kubectl delete namespace "$E2E_NS" --ignore-not-found --timeout=60s 2>/dev/null || true
    kubectl delete gateway "$GATEWAY_NAME" -n "$GATEWAY_NAMESPACE" --ignore-not-found 2>/dev/null || true
    echo "Cleanup complete"
}
trap cleanup EXIT

# ─── Step 1: Preflight checks ─────────────────────────────────────────────

echo ""
echo "=== Step 1: Preflight checks ==="

if ! kubectl cluster-info --request-timeout=10s >/dev/null 2>&1; then
    echo "ERROR: Cannot connect to cluster. Check KUBECONFIG."
    exit 1
fi
echo "  Cluster connectivity: OK"

if ! kubectl get crd gateways.gateway.networking.k8s.io >/dev/null 2>&1; then
    echo "  Gateway API CRDs not found, installing..."
    kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.3.0/standard-install.yaml
    echo "  Gateway API CRDs: installed"
else
    echo "  Gateway API CRDs: OK"
fi

if ! kubectl get deployment istiod -n istio-system >/dev/null 2>&1; then
    echo "  Istio not found, installing ${ISTIO_VERSION}..."
    if ! command -v istioctl &>/dev/null; then
        curl -sL https://istio.io/downloadIstio | ISTIO_VERSION="$ISTIO_VERSION" sh -
        export PATH="$PWD/istio-${ISTIO_VERSION}/bin:$PATH"
    fi
    istioctl install --set profile=minimal \
        --set values.pilot.env.SUPPORT_GATEWAY_API_INFERENCE_EXTENSION=true \
        --set values.pilot.env.ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true \
        -y
    kubectl rollout status deployment/istiod -n istio-system --timeout=120s
    echo "  Istio: installed"
else
    echo "  Istio: OK"
fi

echo "  Checking simulator connectivity..."
if curl --silent --fail --insecure --max-time 10 --output /dev/null \
    --write-out "  Simulator: HTTP %{http_code} (%{time_total}s)\n" \
    "https://${E2E_SIMULATOR_ENDPOINT}/health" 2>/dev/null; then
    echo "  Simulator: reachable"
else
    echo "ERROR: Simulator not reachable at https://${E2E_SIMULATOR_ENDPOINT}/health"
    exit 1
fi

# ─── Step 2: Install CRDs ────────────────────────────────────────────────

echo ""
echo "=== Step 2: Install CRDs ==="

if ! kubectl get crd externalmodels.maas.opendatahub.io >/dev/null 2>&1; then
    echo "  Installing ExternalModel CRD (maas.opendatahub.io)..."
    kubectl apply -f https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml
else
    echo "  ExternalModel CRD (maas.opendatahub.io): already installed"
fi

# ─── Step 3: Create Gateway ──────────────────────────────────────────────

echo ""
echo "=== Step 3: Create Gateway ==="

kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: ${GATEWAY_NAME}
  namespace: ${GATEWAY_NAMESPACE}
spec:
  gatewayClassName: istio
  listeners:
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: All
EOF
echo "  Gateway '$GATEWAY_NAME' applied"

kubectl wait --for=condition=Programmed "gateway/${GATEWAY_NAME}" \
    -n "$GATEWAY_NAMESPACE" --timeout=120s 2>/dev/null && \
    echo "  Gateway programmed" || \
    echo "  WARNING: Gateway not programmed within 120s"

# ─── Step 4: Deploy BBR ──────────────────────────────────────────────────

echo ""
echo "=== Step 4: Deploy payload-processing ==="

# Parse image components for Helm values
IMG_REGISTRY="$(echo "$PAYLOAD_PROCESSING_IMAGE" | cut -d/ -f1)"
IMG_REPO="$(echo "$PAYLOAD_PROCESSING_IMAGE" | cut -d/ -f2- | cut -d: -f1)"
IMG_TAG="$(echo "$PAYLOAD_PROCESSING_IMAGE" | cut -d: -f2)"

if helm status "$HELM_RELEASE" -n "$GATEWAY_NAMESPACE" >/dev/null 2>&1; then
    HELM_CMD="upgrade"
else
    HELM_CMD="install"
fi

helm "$HELM_CMD" "$HELM_RELEASE" "$PROJECT_ROOT/deploy/payload-processing" \
    --namespace "$GATEWAY_NAMESPACE" \
    --create-namespace \
    --dependency-update \
    -f "$SCRIPT_DIR/e2e-values.yaml" \
    --set upstreamBbr.bbr.image.registry="$IMG_REGISTRY" \
    --set upstreamBbr.bbr.image.repository="$IMG_REPO" \
    --set upstreamBbr.bbr.image.tag="$IMG_TAG" \
    --set upstreamBbr.bbr.image.pullPolicy=Always \
    --set upstreamBbr.inferenceGateway.name="$GATEWAY_NAME" \
    --set upstreamBbr.provider.name=istio \
    --set upstreamBbr.provider.istio.envoyFilter.operation=INSERT_FIRST

# Disable Istio sidecar on BBR pod (ext_proc uses self-signed TLS)
kubectl patch deployment payload-processing -n "$GATEWAY_NAMESPACE" --type=merge \
    -p='{"spec":{"template":{"metadata":{"annotations":{"sidecar.istio.io/inject":"false"}}}}}' \
    2>/dev/null || true

kubectl rollout status deployment/payload-processing \
    -n "$GATEWAY_NAMESPACE" --timeout=120s
echo "  Payload processing deployed"

# ─── Step 5: Create test namespace ───────────────────────────────────────

echo ""
echo "=== Step 5: Create test namespace ==="

kubectl create namespace "$E2E_NS" --dry-run=client -o yaml | kubectl apply -f -
echo "  Namespace '$E2E_NS' ready"

# ─── Step 6: Run E2E tests ──────────────────────────────────────────────

echo ""
echo "=== Step 6: Run E2E tests ==="

# Create a ServiceAccount for the test runner
kubectl apply -f - <<EOF
apiVersion: v1
kind: ServiceAccount
metadata:
  name: ${E2E_JOB_NAME}
  namespace: ${E2E_NS}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${E2E_JOB_NAME}-admin
subjects:
- kind: ServiceAccount
  name: ${E2E_JOB_NAME}
  namespace: ${E2E_NS}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cluster-admin
EOF

# Clean up any previous test job
kubectl delete job "$E2E_JOB_NAME" -n "$E2E_NS" --ignore-not-found 2>/dev/null || true

# Resolve the gateway service name for in-cluster curl
GATEWAY_SVC_NAME="${GATEWAY_NAME}-istio"

# Build the Job YAML — use command override to bypass entrypoint.sh's
# KUBECONFIG check (the pod uses in-cluster ServiceAccount auth instead)
LABEL_FILTER_ARG=""
if [[ -n "$E2E_LABEL_FILTER" ]]; then
    LABEL_FILTER_ARG="        - \"-ginkgo.label-filter=$E2E_LABEL_FILTER\""
fi

# Run tests as a Pod (not Job) — the init container runs the tests and writes
# the JUnit report to a shared volume. The main container stays alive so we
# can extract the report via kubectl cp before cleanup.
kubectl delete pod "$E2E_JOB_NAME" -n "$E2E_NS" --ignore-not-found 2>/dev/null || true

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata:
  name: ${E2E_JOB_NAME}
  namespace: ${E2E_NS}
  annotations:
    sidecar.istio.io/inject: "false"
spec:
  serviceAccountName: ${E2E_JOB_NAME}
  restartPolicy: Never
  initContainers:
  - name: e2e
    image: ${PAYLOAD_PROCESSING_E2E_IMAGE}
    imagePullPolicy: IfNotPresent
    command: ["/bin/sh", "-c"]
    args:
    - |
      /e2e/e2e-tests.test \
        -test.v -ginkgo.v -test.count=1 \
        -test.timeout=${E2E_TEST_TIMEOUT} \
        -ginkgo.junit-report=/results/e2e-junit.xml \
        ${LABEL_FILTER_ARG:+"-ginkgo.label-filter=${E2E_LABEL_FILTER}"}
      echo \$? > /results/exit-code
    env:
    - name: E2E_NS
      value: "${E2E_NS}"
    - name: E2E_GATEWAY_NAMESPACE
      value: "${GATEWAY_NAMESPACE}"
    - name: E2E_GATEWAY_NAME
      value: "${GATEWAY_NAME}"
    - name: E2E_GATEWAY_SVC_NAME
      value: "${GATEWAY_SVC_NAME}"
    - name: E2E_SIMULATOR_ENDPOINT
      value: "${E2E_SIMULATOR_ENDPOINT}"
    - name: E2E_SIMULATOR_VALIDATE_KEYS
      value: "true"
    volumeMounts:
    - name: results
      mountPath: /results
  containers:
  - name: results
    image: ${PAYLOAD_PROCESSING_E2E_IMAGE}
    imagePullPolicy: IfNotPresent
    command: ["sleep", "300"]
    volumeMounts:
    - name: results
      mountPath: /results
  volumes:
  - name: results
    emptyDir: {}
EOF

echo "  Test pod created, waiting for tests to complete..."

# Wait for init container to finish (pod transitions to Running when init is done)
if ! kubectl wait --for=condition=Ready "pod/${E2E_JOB_NAME}" \
    -n "$E2E_NS" --timeout=600s 2>/dev/null; then

    POD_PHASE=$(kubectl get pod "$E2E_JOB_NAME" -n "$E2E_NS" \
        -o jsonpath='{.status.phase}' 2>/dev/null)
    INIT_STATUS=$(kubectl get pod "$E2E_JOB_NAME" -n "$E2E_NS" \
        -o jsonpath='{.status.initContainerStatuses[0].state}' 2>/dev/null)

    echo ""
    echo "  Test pod not ready. Phase: $POD_PHASE, Init: $INIT_STATUS"
    echo "  Test logs:"
    echo "  ─────────────────────────────"
    kubectl logs "$E2E_JOB_NAME" -n "$E2E_NS" -c e2e --tail=50 2>/dev/null || true
fi

# Print test logs from the init container
echo ""
echo "  Test output (last 30 lines):"
echo "  ─────────────────────────────"
kubectl logs "$E2E_JOB_NAME" -n "$E2E_NS" -c e2e --tail=30 2>/dev/null || true

# Check test exit code
TEST_EXIT_CODE=$(kubectl exec "$E2E_JOB_NAME" -n "$E2E_NS" -c results \
    -- cat /results/exit-code 2>/dev/null || echo "1")
TEST_EXIT=${TEST_EXIT_CODE:-1}

# ─── Step 7: Collect results ─────────────────────────────────────────────

echo ""
echo "=== Step 7: Collect results ==="

mkdir -p "$(dirname "$E2E_JUNIT_REPORT")"

kubectl exec "$E2E_JOB_NAME" -n "$E2E_NS" -c results \
    -- cat /results/e2e-junit.xml > "$E2E_JUNIT_REPORT" 2>/dev/null && \
    echo "  JUnit report saved: $E2E_JUNIT_REPORT ($(wc -c < "$E2E_JUNIT_REPORT" | tr -d ' ') bytes)" || \
    echo "  WARNING: Could not extract JUnit report"

echo ""
echo "================================================"
if [[ $TEST_EXIT -eq 0 ]]; then
    echo "  E2E Tests: PASSED"
else
    echo "  E2E Tests: FAILED"
fi
echo "  JUnit Report: $E2E_JUNIT_REPORT"
echo "================================================"

exit "$TEST_EXIT"
