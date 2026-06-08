#!/bin/bash
# Download Istio, Gateway API, and legacy MaaS CRDs for envtest.
#
# Usage:
#   ./hack/download-test-crds.sh [output-dir]
#
# The CRDs are cached — re-run is a no-op unless the output directory is deleted.

set -euo pipefail

ISTIO_VERSION="${ISTIO_VERSION:-1.25.2}"
GATEWAY_API_VERSION="${GATEWAY_API_VERSION:-v1.3.0}"

OUTPUT_DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/test/testdata/crds}"

if [ -d "$OUTPUT_DIR/istio" ] && [ -d "$OUTPUT_DIR/gateway-api" ] && [ -d "$OUTPUT_DIR/legacy" ]; then
    exit 0
fi

echo "Downloading test CRDs to ${OUTPUT_DIR}..."
mkdir -p "$OUTPUT_DIR/istio" "$OUTPUT_DIR/gateway-api" "$OUTPUT_DIR/legacy"

curl -sSfL "https://raw.githubusercontent.com/istio/istio/${ISTIO_VERSION}/manifests/charts/base/files/crd-all.gen.yaml" \
    -o "$OUTPUT_DIR/istio/crd-all.gen.yaml"

GWAPI_BASE="https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}"
curl -sSfL "${GWAPI_BASE}/standard-install.yaml" -o "$OUTPUT_DIR/gateway-api/standard-install.yaml"

curl -sSfL "https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml" \
    -o "$OUTPUT_DIR/legacy/maas-externalmodel-crd.yaml"

echo "Done: $(find "$OUTPUT_DIR" -name '*.yaml' | wc -l | tr -d ' ') CRD files downloaded"
