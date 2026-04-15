#!/bin/bash
# Set up a Kind cluster with Istio and BBR for E2E testing.
#
# Prerequisites: docker, helm
#
# Usage:
#   ./test/e2e/scripts/setup-kind.sh
#
# Environment variables:
#   KIND_CLUSTER_NAME    - Kind cluster name (default: bbr-e2e)
#   ISTIO_VERSION        - Istio version (default: 1.27.0-alpha.0)
#   E2E_SIMULATOR_ENDPOINT - Simulator IP (default: 3.13.21.181)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-bbr-e2e}"
ISTIO_VERSION="${ISTIO_VERSION:-1.29.2}"
SIMULATOR_ENDPOINT="${E2E_SIMULATOR_ENDPOINT:-3.13.21.181}"
GATEWAY_NAMESPACE="default"
GATEWAY_NAME="e2e-gateway"

# ─── Install tools ───────────────────────────────────────────────────────────

install_kind() {
    if command -v kind &>/dev/null; then
        echo "kind already installed: $(kind version)"
        return
    fi
    echo "Installing kind..."
    go install sigs.k8s.io/kind@v0.29.0
}

install_istioctl() {
    if command -v istioctl &>/dev/null; then
        echo "istioctl already installed: $(istioctl version --remote=false 2>/dev/null || echo 'unknown')"
        return
    fi
    echo "Installing istioctl ${ISTIO_VERSION}..."
    curl -sL https://istio.io/downloadIstio | ISTIO_VERSION="$ISTIO_VERSION" sh -
    export PATH="$PWD/istio-${ISTIO_VERSION}/bin:$PATH"
}

# ─── Cluster setup ───────────────────────────────────────────────────────────

create_kind_cluster() {
    if kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
        echo "Kind cluster '${KIND_CLUSTER_NAME}' already exists"
        kubectl cluster-info --context "kind-${KIND_CLUSTER_NAME}" &>/dev/null || {
            echo "Cluster exists but not reachable, recreating..."
            kind delete cluster --name "$KIND_CLUSTER_NAME"
        }
    fi

    if ! kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
        echo "Creating Kind cluster '${KIND_CLUSTER_NAME}'..."
        kind create cluster --name "$KIND_CLUSTER_NAME" --wait 120s
    fi

    kubectl config use-context "kind-${KIND_CLUSTER_NAME}"
    echo "Cluster ready"
}

install_istio() {
    echo "Installing Istio ${ISTIO_VERSION}..."
    istioctl install --set profile=minimal \
        --set values.pilot.env.SUPPORT_GATEWAY_API_INFERENCE_EXTENSION=true \
        --set values.pilot.env.ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true \
        -y

    # Wait for istiod
    kubectl rollout status deployment/istiod -n istio-system --timeout=120s
    echo "Istio ready"
}

install_gateway_api_crds() {
    echo "Installing Gateway API CRDs..."
    kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.3.0/standard-install.yaml
    echo "Gateway API CRDs installed"
}

install_external_model_crd() {
    echo "Installing ExternalModel CRD..."
    kubectl apply -f https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml
    echo "ExternalModel CRD installed"
}

create_gateway() {
    echo "Creating Gateway '${GATEWAY_NAME}'..."
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

    echo "Waiting for Gateway to be programmed..."
    kubectl wait --for=condition=Programmed "gateway/${GATEWAY_NAME}" \
        -n "$GATEWAY_NAMESPACE" --timeout=120s || {
        echo "WARNING: Gateway not programmed after 120s, continuing..."
    }
    echo "Gateway ready"
}

deploy_bbr() {
    echo "Deploying BBR via Helm chart..."

    # Build and load image for arm64 if on Mac (quay.io image is x86 only)
    if [[ "$(uname -m)" == "arm64" ]]; then
        echo "  Building arm64 image for Kind..."
        (cd "$PROJECT_ROOT" && make image-local-load \
            IMAGE_TAG=quay.io/opendatahub/odh-ai-gateway-payload-processing:odh-stable \
            PLATFORMS=linux/arm64 2>/dev/null)
        kind load docker-image quay.io/opendatahub/odh-ai-gateway-payload-processing:odh-stable \
            --name "$KIND_CLUSTER_NAME"
    fi

    helm install payload-processing "$PROJECT_ROOT/deploy/payload-processing" \
        --namespace "$GATEWAY_NAMESPACE" \
        --dependency-update \
        --set upstreamBbr.inferenceGateway.name="$GATEWAY_NAME" \
        --set upstreamBbr.provider.name=istio \
        --set upstreamBbr.provider.istio.envoyFilter.operation=INSERT_FIRST

    # Disable sidecar injection on BBR pod (ext_proc uses self-signed TLS,
    # sidecar intercepts and breaks the connection)
    kubectl patch deployment payload-processing -n "$GATEWAY_NAMESPACE" --type=merge \
        -p='{"spec":{"template":{"metadata":{"annotations":{"sidecar.istio.io/inject":"false"}}}}}'

    kubectl rollout status deployment/payload-processing \
        -n "$GATEWAY_NAMESPACE" --timeout=120s
    echo "BBR deployed"
}

create_test_namespace() {
    local ns="${1:-e2e-models}"
    if ! kubectl get namespace "$ns" &>/dev/null; then
        kubectl create namespace "$ns"
        kubectl label namespace "$ns" istio-injection=enabled --overwrite
    fi
    echo "Namespace '$ns' ready"
}

# ─── Main ────────────────────────────────────────────────────────────────────

main() {
    echo "=== BBR E2E Test Environment Setup ==="
    echo "  Cluster:   ${KIND_CLUSTER_NAME}"
    echo "  Istio:     ${ISTIO_VERSION}"
    echo "  Simulator: ${SIMULATOR_ENDPOINT}"
    echo ""

    install_kind
    install_istioctl
    create_kind_cluster
    install_gateway_api_crds
    install_istio
    install_external_model_crd
    create_gateway
    create_test_namespace "e2e-models"
    deploy_bbr

    echo ""
    echo "=== Setup Complete ==="
    echo "  Gateway: ${GATEWAY_NAME} in ${GATEWAY_NAMESPACE}"
    echo "  BBR: payload-processing deployed"
    echo "  Run: make test-e2e"
}

main "$@"
