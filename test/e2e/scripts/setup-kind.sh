#!/bin/bash
# Set up a Kind cluster with Istio and BBR for E2E testing.
#
# Prerequisites: docker, helm
#
# Usage:
#   ./test/e2e/scripts/setup-kind.sh
#
# Environment variables:
#   KIND_CLUSTER_NAME    - Kind cluster name (default: ipp-e2e)
#   ISTIO_VERSION        - Istio version (default: 1.29.2)
#   E2E_SIMULATOR_ENDPOINT - Simulator IP (default: 3.147.232.199)
#   GATEWAY_TLS_SECRET   - If set, add an HTTPS listener (port 443) using this TLS Secret
#   GATEWAY_PARAMS_CONFIGMAP - If set, reference this ConfigMap via spec.infrastructure.parametersRef
#   CLUSTER_NAME         - If set, passed to the IPP pod env (routing loop detection)
#   SKIP_IMAGE_BUILD     - If "true", reuse the already-built IPP image (still kind-loaded)
#
# This file can also be sourced for its functions (setup-kind-cross-cluster.sh does).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-ipp-e2e}"
ISTIO_VERSION="${ISTIO_VERSION:-1.29.2}"
SIMULATOR_ENDPOINT="${E2E_SIMULATOR_ENDPOINT:-3.147.232.199}"
GATEWAY_NAMESPACE="${GATEWAY_NAMESPACE:-default}"
GATEWAY_NAME="${GATEWAY_NAME:-e2e-gateway}"
IPP_IMAGE_TAG="quay.io/opendatahub/odh-ai-gateway-payload-processing:latest"

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
    local istio_dir="$PROJECT_ROOT/bin/istio-${ISTIO_VERSION}"
    if [[ -x "$istio_dir/bin/istioctl" ]]; then
        export PATH="$istio_dir/bin:$PATH"
    fi
    if command -v istioctl &>/dev/null; then
        echo "istioctl already installed: $(istioctl version --remote=false 2>/dev/null || echo 'unknown')"
        return
    fi
    echo "Installing istioctl ${ISTIO_VERSION} into $PROJECT_ROOT/bin..."
    mkdir -p "$PROJECT_ROOT/bin"
    (cd "$PROJECT_ROOT/bin" && curl -sL https://istio.io/downloadIstio | ISTIO_VERSION="$ISTIO_VERSION" sh -)
    export PATH="$istio_dir/bin:$PATH"
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
        --set values.pilot.env.ENABLE_GATEWAY_API_INFERENCE_EXTENSION=true \
        -y

    # Wait for istiod
    kubectl rollout status deployment/istiod -n istio-system --timeout=120s
    echo "Istio ready"
}

install_gateway_api_crds() {
    echo "Installing Gateway API CRDs..."
    kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.0/standard-install.yaml
    echo "Gateway API CRDs installed"
}

install_external_model_crd() {
    echo "Installing Legacy ExternalModel CRD..."
    kubectl apply -f https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml
    echo "ExternalModel CRD installed"

    echo "Installing inference.opendatahub.io CRDs..."
    kubectl apply -f "$PROJECT_ROOT/config/crd/bases/"
    echo "inference.opendatahub.io CRDs installed"
}

create_gateway() {
    echo "Creating Gateway '${GATEWAY_NAME}'..."
    local infrastructure="" https_listener=""
    if [[ -n "${GATEWAY_PARAMS_CONFIGMAP:-}" ]]; then
        infrastructure="  infrastructure:
    parametersRef:
      group: \"\"
      kind: ConfigMap
      name: ${GATEWAY_PARAMS_CONFIGMAP}
"
    fi
    if [[ -n "${GATEWAY_TLS_SECRET:-}" ]]; then
        https_listener="  - name: https
    port: 443
    protocol: HTTPS
    tls:
      mode: Terminate
      certificateRefs:
      - name: ${GATEWAY_TLS_SECRET}
    allowedRoutes:
      namespaces:
        from: All
"
    fi
    kubectl apply -f - <<EOF
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: ${GATEWAY_NAME}
  namespace: ${GATEWAY_NAMESPACE}
spec:
  gatewayClassName: istio
${infrastructure}  listeners:
  - name: http
    port: 80
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: All
${https_listener}
EOF

    # Kind has no LoadBalancer, so the Gateway never reports Programmed;
    # the generated Deployment being available is the usable signal.
    echo "Waiting for Gateway deployment..."
    for _ in $(seq 1 60); do
        kubectl get deployment "${GATEWAY_NAME}-istio" -n "$GATEWAY_NAMESPACE" &>/dev/null && break
        sleep 2
    done
    kubectl rollout status deployment/"${GATEWAY_NAME}-istio" \
        -n "$GATEWAY_NAMESPACE" --timeout=180s
    echo "Gateway ready"
}

build_ipp_image() {
    echo "  Building IPP image from source..."
    local platform="linux/amd64"
    if [[ "$(uname -m)" == "arm64" ]]; then
        platform="linux/arm64"
    fi
    docker build -t "$IPP_IMAGE_TAG" --platform "$platform" "$PROJECT_ROOT"
}

deploy_ipp() {
    echo "Deploying IPP via Helm chart..."

    local image_tag="$IPP_IMAGE_TAG"

    # Build from source so E2E tests run against the current code; the
    # quay.io image may not include the PR being tested.
    if [[ "${SKIP_IMAGE_BUILD:-}" != "true" ]]; then
        build_ipp_image
    fi
    kind load docker-image "$image_tag" --name "$KIND_CLUSTER_NAME"

    local extra_env=()
    if [[ -n "${CLUSTER_NAME:-}" ]]; then
        extra_env=(--set upstreamIpp.payloadProcessor.env[2].name=CLUSTER_NAME
                   --set upstreamIpp.payloadProcessor.env[2].value="$CLUSTER_NAME")
    fi

    helm install payload-processing "$PROJECT_ROOT/deploy/payload-processing" \
        --namespace "$GATEWAY_NAMESPACE" \
        --dependency-update \
        -f "$SCRIPT_DIR/e2e-values.yaml" \
        --set upstreamIpp.inferenceGateway.name="$GATEWAY_NAME" \
        --set upstreamIpp.payloadProcessor.env[0].name=GATEWAY_NAME \
        --set upstreamIpp.payloadProcessor.env[0].value="$GATEWAY_NAME" \
        --set upstreamIpp.payloadProcessor.env[1].name=GATEWAY_NAMESPACE \
        --set upstreamIpp.payloadProcessor.env[1].value="$GATEWAY_NAMESPACE" \
        "${extra_env[@]}"

    # Disable sidecar injection on IPP pod
    kubectl patch deployment payload-processing -n "$GATEWAY_NAMESPACE" --type=merge \
        -p='{"spec":{"template":{"metadata":{"annotations":{"sidecar.istio.io/inject":"false"}}}}}'

    kubectl rollout status deployment/payload-processing \
        -n "$GATEWAY_NAMESPACE" --timeout=120s
    echo "IPP deployed"
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
    echo "=== IPP E2E Test Environment Setup ==="
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
    deploy_ipp

    echo ""
    echo "=== Setup Complete ==="
    echo "  Gateway: ${GATEWAY_NAME} in ${GATEWAY_NAMESPACE}"
    echo "  IPP: payload-processing deployed"
    echo "  Run: make test-e2e"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
