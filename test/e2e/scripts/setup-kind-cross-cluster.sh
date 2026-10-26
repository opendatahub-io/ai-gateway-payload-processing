#!/bin/bash
# Set up two Kind clusters (A and B) for the cross-cluster routing E2E (issue #344).
#
# Topology:
#   test (host) --HTTP--> gateway A --TLS--> gateway B --TLS--> echo backend (docker)
#
# Each cluster runs Istio, a Gateway API Gateway with an HTTP listener (for
# the test client) and an HTTPS listener (for the other cluster), and the IPP
# with CLUSTER_NAME set so routing loop detection is active. The clusters
# reach each other over the shared "kind" docker network using node IP +
# NodePort; hostnames come from sslip.io, the same trick the single-cluster
# E2E uses for the simulator.
#
# TLS: ExternalProvider requires auth, and the controller refuses plaintext
# with auth (CWE-319), so every hop is TLS. A throwaway CA signs one wildcard
# cert for *.sslip.io; the CA is mounted into each gateway proxy's system
# trust bundle via the Gateway's infrastructure.parametersRef overlay. Product
# support for custom CA trust / skip-verify is RHAIRFE-2243 / RHAIRFE-2242.
#
# Prerequisites: docker, kind, kubectl, helm, openssl
#
# Usage:
#   ./test/e2e/scripts/setup-kind-cross-cluster.sh
#   set -a; . bin/cross-cluster.env; set +a
#   go test ./test/e2e/crosscluster/ -v
#
# Environment variables:
#   E2E_CC_CLUSTER_A / E2E_CC_CLUSTER_B - Kind cluster names (default: ipp-e2e-a / ipp-e2e-b)
#   ISTIO_VERSION                       - Istio version (default: see setup-kind.sh)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# shellcheck source=setup-kind.sh
source "$SCRIPT_DIR/setup-kind.sh"

CLUSTER_A="${E2E_CC_CLUSTER_A:-ipp-e2e-a}"
CLUSTER_B="${E2E_CC_CLUSTER_B:-ipp-e2e-b}"
ECHO_CONTAINER="ipp-e2e-echo"
ECHO_IMAGE="registry.access.redhat.com/ubi9/python-311:latest"
ECHO_PORT=8443
TLS_SECRET="e2e-gateway-tls"
CA_CONFIGMAP="e2e-ca-bundle"
GATEWAY_PARAMS="e2e-gateway-params"
CERT_DIR="$PROJECT_ROOT/bin/cross-cluster-certs"
ENV_FILE="$PROJECT_ROOT/bin/cross-cluster.env"

# ─── Certificates ────────────────────────────────────────────────────────────

generate_certs() {
    if [[ -f "$CERT_DIR/server.crt" ]]; then
        echo "Certificates already present in $CERT_DIR"
        return
    fi
    echo "Generating throwaway CA and *.sslip.io server certificate..."
    mkdir -p "$CERT_DIR"
    openssl req -x509 -newkey rsa:2048 -nodes -days 7 -subj "/CN=ipp-e2e-ca" \
        -keyout "$CERT_DIR/ca.key" -out "$CERT_DIR/ca.crt" 2>/dev/null
    openssl req -newkey rsa:2048 -nodes -subj "/CN=ipp-e2e" \
        -addext "subjectAltName=DNS:*.sslip.io" \
        -keyout "$CERT_DIR/server.key" -out "$CERT_DIR/server.csr" 2>/dev/null
    openssl x509 -req -days 7 -in "$CERT_DIR/server.csr" \
        -CA "$CERT_DIR/ca.crt" -CAkey "$CERT_DIR/ca.key" -CAcreateserial \
        -copy_extensions copy -out "$CERT_DIR/server.crt" 2>/dev/null
    chmod 644 "$CERT_DIR"/*
}

# Trust material for one cluster: TLS Secret for the HTTPS listener, CA
# bundle ConfigMap, and the Gateway deployment overlay that mounts the CA
# over the proxy's system trust bundle (used for TLS origination).
install_trust() {
    kubectl create secret tls "$TLS_SECRET" -n "$GATEWAY_NAMESPACE" \
        --cert="$CERT_DIR/server.crt" --key="$CERT_DIR/server.key" \
        --dry-run=client -o yaml | kubectl apply -f -
    kubectl create configmap "$CA_CONFIGMAP" -n "$GATEWAY_NAMESPACE" \
        --from-file=ca-certificates.crt="$CERT_DIR/ca.crt" \
        --dry-run=client -o yaml | kubectl apply -f -
    kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: ${GATEWAY_PARAMS}
  namespace: ${GATEWAY_NAMESPACE}
data:
  deployment: |
    spec:
      template:
        spec:
          volumes:
          - name: e2e-ca-bundle
            configMap:
              name: ${CA_CONFIGMAP}
          containers:
          - name: istio-proxy
            volumeMounts:
            - name: e2e-ca-bundle
              mountPath: /etc/ssl/certs/ca-certificates.crt
              subPath: ca-certificates.crt
EOF
}

# ─── Clusters ────────────────────────────────────────────────────────────────

setup_cluster() {
    local name="$1"
    echo ""
    echo "=== Cluster ${name} ==="
    KIND_CLUSTER_NAME="$name" create_kind_cluster
    install_gateway_api_crds
    install_istio
    install_external_model_crd
    install_trust
    GATEWAY_TLS_SECRET="$TLS_SECRET" GATEWAY_PARAMS_CONFIGMAP="$GATEWAY_PARAMS" create_gateway
    KIND_CLUSTER_NAME="$name" CLUSTER_NAME="$name" SKIP_IMAGE_BUILD=true deploy_ipp
}

node_ip() {
    kubectl --context "kind-$1" get nodes \
        -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}'
}

node_port() {
    kubectl --context "kind-$1" get svc "${GATEWAY_NAME}-istio" -n "$GATEWAY_NAMESPACE" \
        -o jsonpath="{.spec.ports[?(@.name==\"$2\")].nodePort}"
}

sslip_host() {
    echo "${1//./-}.sslip.io"
}

# ─── Echo backend ────────────────────────────────────────────────────────────

start_echo() {
    echo "Starting echo backend container '${ECHO_CONTAINER}'..."
    docker rm -f "$ECHO_CONTAINER" &>/dev/null || true
    docker run -d --name "$ECHO_CONTAINER" --network kind \
        -v "$CERT_DIR:/certs:ro,z" \
        -v "$SCRIPT_DIR/echo-server.py:/echo-server.py:ro,z" \
        "$ECHO_IMAGE" python3 /echo-server.py >/dev/null
    ECHO_IP="$(docker inspect -f '{{.NetworkSettings.Networks.kind.IPAddress}}' "$ECHO_CONTAINER")"
    echo "Echo backend at ${ECHO_IP}:${ECHO_PORT}"
}

# ─── Main ────────────────────────────────────────────────────────────────────

main() {
    echo "=== IPP Cross-Cluster E2E Environment Setup ==="
    echo "  Cluster A: ${CLUSTER_A}"
    echo "  Cluster B: ${CLUSTER_B}"
    echo "  Istio:     ${ISTIO_VERSION}"
    echo ""

    install_kind
    install_istioctl
    generate_certs
    build_ipp_image
    setup_cluster "$CLUSTER_A"
    setup_cluster "$CLUSTER_B"
    # After the clusters: kind creates the "kind" docker network on first use.
    start_echo

    local ip_a ip_b
    ip_a="$(node_ip "$CLUSTER_A")"
    ip_b="$(node_ip "$CLUSTER_B")"

    mkdir -p "$(dirname "$ENV_FILE")"
    cat > "$ENV_FILE" <<EOF
E2E_CC_CONTEXT_A=kind-${CLUSTER_A}
E2E_CC_CONTEXT_B=kind-${CLUSTER_B}
E2E_CC_CLUSTER_NAME_A=${CLUSTER_A}
E2E_CC_CLUSTER_NAME_B=${CLUSTER_B}
E2E_CC_GATEWAY_A_URL=http://${ip_a}:$(node_port "$CLUSTER_A" http)
E2E_CC_GATEWAY_A_HOST=$(sslip_host "$ip_a")
E2E_CC_GATEWAY_A_HTTPS_PORT=$(node_port "$CLUSTER_A" https)
E2E_CC_GATEWAY_B_HOST=$(sslip_host "$ip_b")
E2E_CC_GATEWAY_B_HTTPS_PORT=$(node_port "$CLUSTER_B" https)
E2E_CC_ECHO_HOST=$(sslip_host "$ECHO_IP")
E2E_CC_ECHO_PORT=${ECHO_PORT}
EOF

    echo ""
    echo "=== Setup Complete ==="
    cat "$ENV_FILE"
    echo ""
    echo "  Run: make test-e2e-cross-cluster"
}

main "$@"
