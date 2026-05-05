# E2E Integration Tests

End-to-end integration tests for ExternalProvider/ExternalModel CRDs (`inference.opendatahub.io/v1alpha1`).

These tests send **real HTTP requests** through the full gateway stack:
Envoy → Kuadrant/Authorino → BBR (payload-processing) → external model endpoint (llm-katan).

No mocks, no unit test fakes — every test validates live cluster behavior.

## What's Covered

| Category | File | Tests | Status |
|----------|------|-------|--------|
| **Reconciler: resource creation** | `test_reconciler.py` | Provider creates Service, ServiceEntry, DestinationRule; Model creates HTTPRoute; ownership, labels, gateway targeting | Pass |
| **Reconciler: negative cases** | `test_reconciler.py` | Model with non-existent provider ref; Provider with missing Secret | Pass |
| **Reconciler: multiple providers** | `test_reconciler.py` | OpenAI, Anthropic, Vertex providers/models all reconcile to Ready | Pass |
| **Auth: negative** | `test_auth.py` | No auth → 401; invalid bearer → 401; fake API key → 401; random auth → 401 | Pass (requires gateway-default-auth) |
| **Auth: positive** | `test_auth.py` | Valid API key → 200; response has choices, model field, non-empty content | Pass (requires MaaSModelRef + AuthPolicy + Subscription) |
| **Auth: error paths** | `test_auth.py` | Wrong model name in body → 404; unsupported path (/embeddings) → 400; empty messages; non-existent route | Pass |
| **Lifecycle** | `test_lifecycle.py` | Delete ExternalModel → HTTPRoute removed; delete provider → model goes Failed; recreate provider → model recovers | Pass |
| **Multi-provider weights** | `test_multiprovider.py` | Multiple provider refs, weighted traffic splitting, X-Selected-Provider header | xfail (PR #213 not merged) |
| **Migration v1alpha1 → v1alpha2** | `test_migration.py` | Auto-conversion of old ExternalModel CRs, credential preservation, provider deduplication | xfail (not implemented) |

Tests marked `xfail` run but are expected to fail — they track unimplemented features.
When a feature lands and the test starts passing, pytest flags it as `XPASS`, signaling the marker should be removed.

## Prerequisites

### Cluster requirements (both Kind and OpenShift)

- Istio with Gateway API support
- Gateway named `maas-default-gateway` in the gateway namespace
- Kuadrant operator + Authorino (for auth tests)
- BBR (payload-processing) deployed with `model-provider-resolver` plugin
- `inference.opendatahub.io` CRDs installed
- ExternalProvider + ExternalModel CRs deployed and reconciled
- An external model endpoint reachable from the cluster (e.g., llm-katan)

### For auth tests (test_auth.py)

- Gateway-level default-deny AuthPolicy (`gateway-default-auth`) applied
- MaaS controller deployed (with [PR #865](https://github.com/opendatahub-io/models-as-a-service/pull/865) fix for API-group agnostic MaaSModelRef)
- MaaSModelRef pointing at the ExternalModel (kind: ExternalModel, name matches)
- MaaSAuthPolicy granting access to the model
- MaaSSubscription with token budget
- MaaS API reachable at `{GATEWAY_HOST}/maas-api` (for API key creation)

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `GATEWAY_HOST` | **Yes** | — | Gateway endpoint (e.g., `localhost:19080` or `maas.example.com`) |
| `E2E_SIMULATOR_ENDPOINT` | For lifecycle tests | — | llm-katan FQDN (e.g., `3-13-21-181.sslip.io`) |
| `E2E_MODEL_NAMESPACE` | No | `llm` | Namespace where ExternalProvider/Model CRs live |
| `E2E_NEW_CRD_MODEL` | No | `new-katan-openai` | ExternalModel name for reconciler/auth tests |
| `E2E_NEW_CRD_PROVIDER` | No | `katan-openai-provider` | ExternalProvider name for reconciler tests |
| `E2E_NEW_CRD_TARGET_MODEL` | No | `llm-katan-echo` | targetModel value for request body |
| `E2E_NEW_CRD_SUBSCRIPTION` | No | `new-crd-subscription` | MaaSSubscription name for API key creation |
| `E2E_MULTI_PROVIDER_MODEL` | No | `multi-provider-test` | ExternalModel with multiple provider refs |
| `INSECURE_HTTP` | No | `false` | Use HTTP instead of HTTPS (for Kind port-forward) |
| `E2E_SKIP_TLS_VERIFY` | No | `false` | Skip TLS certificate verification |
| `E2E_TIMEOUT` | No | `30` | HTTP request timeout in seconds |

## Running on Kind (local-deploy)

```bash
# 1. Deploy the cluster using local-deploy.sh (from models-as-a-service repo)
#    This sets up Istio, Kuadrant, MaaS, BBR, and test fixtures.

# 2. Port-forward the gateway
kubectl port-forward -n istio-system svc/maas-default-gateway-istio 19080:80 &

# 3. Install test dependencies
pip install -r test/e2e-integration/requirements.txt

# 4. Run all tests
GATEWAY_HOST="localhost:19080" \
INSECURE_HTTP="true" \
E2E_SKIP_TLS_VERIFY="true" \
E2E_SIMULATOR_ENDPOINT="3-13-21-181.sslip.io" \
E2E_MODEL_NAMESPACE="llm" \
  python -m pytest test/e2e-integration/ -v

# Run a specific category
python -m pytest test/e2e-integration/test_auth.py -v

# Run only passing tests (skip xfail)
python -m pytest test/e2e-integration/ -v -m "not xfail_known"
```

## Running on OpenShift

The same tests work on OpenShift — the only differences are the gateway endpoint and TLS:

```bash
# 1. Ensure you're logged in to the OpenShift cluster
oc login ...

# 2. Get the gateway hostname
GATEWAY_HOST=$(oc get gateway maas-default-gateway -n openshift-ingress -o jsonpath='{.status.addresses[0].value}')

# 3. Run tests (HTTPS by default, no INSECURE_HTTP)
GATEWAY_HOST="$GATEWAY_HOST" \
E2E_SIMULATOR_ENDPOINT="3-13-21-181.sslip.io" \
E2E_MODEL_NAMESPACE="llm" \
  python -m pytest test/e2e-integration/ -v
```

Key differences from Kind:
- **No `INSECURE_HTTP`** — OpenShift gateways use HTTPS with valid certs
- **No `E2E_SKIP_TLS_VERIFY`** — TLS certs are valid (unless self-signed)
- **Gateway hostname** — use the actual route/LB hostname, not localhost port-forward
- **Auth** — same flow, but the gateway-default-auth AuthPolicy should already be deployed by the MaaS operator

## Test Design Principles

- **No mocks** — all tests hit the real gateway and validate real HTTP responses
- **Standalone** — no imports from MaaS repo; helpers use `kubectl` and `requests` directly
- **Idempotent** — tests that create resources clean up after themselves
- **Descriptive failures** — assertion messages explain what went wrong and what to check
- **xfail for gaps** — unimplemented features are tracked with `pytest.mark.xfail(reason=...)` referencing the blocking PR/issue
