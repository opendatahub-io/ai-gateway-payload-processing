#!/bin/bash
# E2E testing orchestrator script for AI Gateway Payload Processing.

set -euo pipefail

E2E_SIMULATOR_ENDPOINT="${E2E_SIMULATOR_ENDPOINT:-3-13-21-181.sslip.io}"
PAYLOAD_PROCESSING_IMAGE="${PAYLOAD_PROCESSING_IMAGE:-quay.io/opendatahub/ai-gateway-payload-processing:odh-stable}"
PAYLOAD_PROCESSING_E2E_IMAGE="${PAYLOAD_PROCESSING_E2E_IMAGE:-quay.io/opendatahub/ai-gateway-payload-processing-e2e:odh-stable}"


echo "================================================"
echo "Payload Processing E2E Testing"
echo "================================================"
echo "Test Configuration:"
echo "  E2E Simulator Endpoint: $E2E_SIMULATOR_ENDPOINT"
echo "  Payload Processing Image: $PAYLOAD_PROCESSING_IMAGE"
echo "  Payload Processing E2E Image: $PAYLOAD_PROCESSING_E2E_IMAGE"
echo "================================================"

echo "Checking simulator connectivity..."
if curl --silent --fail --max-time 10 --output /dev/null --write-out "HTTP %{http_code} | time: %{time_total}s | ip: %{remote_ip}\n" "https://${E2E_SIMULATOR_ENDPOINT}/health"; then
    reachable=true
    echo "Simulator is reachable"
else
    reachable=false
    echo "Simulator is NOT reachable at https://${E2E_SIMULATOR_ENDPOINT}/health"
    echo "Payload Processing E2E Testing Failed"
    exit 1
fi

#  TODO: Add Deployment and E2E tests
