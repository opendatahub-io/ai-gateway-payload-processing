#!/bin/bash
# Entrypoint for the E2E test container image.
# Used by the RHOAI shift-left Jenkins pipeline via `podman run`.
#
# Environment variables:
#   KUBECONFIG               - Path to kubeconfig file (mounted by Jenkins)
#   E2E_LABEL_FILTER         - Ginkgo label filter (e.g. "smoke", "tier1", "tier1 || tier2")
#   E2E_JUNIT_REPORT         - JUnit report path (default: /results/results_e2e_xunit.xml)
#   E2E_NS                   - Test namespace (default: bbr-e2e)
#   E2E_GATEWAY_NAMESPACE    - Gateway namespace (default: default)
#   E2E_GATEWAY_NAME         - Gateway name (default: e2e-gateway)
#   E2E_SIMULATOR_ENDPOINT   - Simulator IP/host (default: 3.13.21.181)
#   E2E_SIMULATOR_VALIDATE_KEYS - Enable key validation tests (true/false)

set -euo pipefail

REPORT_PATH="${E2E_JUNIT_REPORT:-/results/results_e2e_xunit.xml}"

# Ensure the report directory exists.
mkdir -p "$(dirname "$REPORT_PATH")"

# Validate kubeconfig.
if [[ -z "${KUBECONFIG:-}" ]]; then
    echo "ERROR: KUBECONFIG environment variable is not set."
    echo "Mount a kubeconfig file and set KUBECONFIG to its path."
    exit 1
fi

if [[ ! -f "$KUBECONFIG" ]]; then
    echo "ERROR: KUBECONFIG file not found at: $KUBECONFIG"
    exit 1
fi

echo "Verifying cluster connectivity..."
if ! kubectl cluster-info --request-timeout=10s >/dev/null 2>&1; then
    echo "ERROR: Cannot connect to the cluster. Check KUBECONFIG."
    exit 1
fi
echo "Cluster connectivity OK."

# Ensure ExternalModel CRD is installed (required for E2E tests).
if ! kubectl get crd externalmodels.maas.opendatahub.io >/dev/null 2>&1; then
    echo "ExternalModel CRD not found, installing..."
    kubectl apply -f https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml
    echo "ExternalModel CRD installed."
else
    echo "ExternalModel CRD already installed."
fi

# Build test arguments.
TEST_ARGS=(
    -test.v
    -ginkgo.v
    -test.count=1
    -test.timeout=10m
    "-ginkgo.junit-report=$REPORT_PATH"
)

if [[ -n "${E2E_LABEL_FILTER:-}" ]]; then
    TEST_ARGS+=("-ginkgo.label-filter=$E2E_LABEL_FILTER")
    echo "Label filter: $E2E_LABEL_FILTER"
fi

echo "Running E2E tests..."
echo "Report: $REPORT_PATH"
echo ""

# Run the compiled test binary. Exit with its exit code.
exec /e2e/e2e-tests.test "${TEST_ARGS[@]}"
