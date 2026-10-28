# Remote MaaS Cluster (`remote-maas`)

## Overview

The `remote-maas` provider routes requests to a model served by **another
RHOAI/ODH cluster** through that cluster's MaaS gateway. From the local
cluster's point of view the remote gateway is just another external
OpenAI-compatible endpoint, so the same ExternalProvider/ExternalModel flow
applies. Two things are specific to this provider:

- **Loop detection.** Each outgoing `remote-maas` hop carries an
  `X-Origin-Cluster` header naming the cluster the request started from. A
  cluster that receives a request carrying its own name rejects it with
  HTTP 403 instead of forwarding it again. This only works when
  `CLUSTER_NAME` is set on the payload-processing deployment of **both**
  clusters.
- **Credential swap.** The client authenticates to the local gateway with a
  local MaaS key. The payload processor replaces that key with the remote
  cluster's key stored in the referenced Secret before forwarding, so the
  remote gateway enforces its own auth and rate limits on that key.

Throughout this guide, **Cluster A** is the cluster the client talks to and
**Cluster B** hosts the model.

```
client ──HTTPS──▶ Gateway A (IPP, CLUSTER_NAME=cluster-a)
                     │  Authorization: Bearer <cluster-b MaaS key>
                     │  X-Origin-Cluster: cluster-a
                     ▼
                  Gateway B (IPP, CLUSTER_NAME=cluster-b) ──▶ model on B
```

## Configuration

| Field | Value |
|-------|-------|
| Provider type | `remote-maas` |
| Endpoint | Cluster B's MaaS gateway hostname, e.g. `maas.apps.cluster-b.example.com` (DNS name, not an IP) |
| Port | `443` by default; set `inference.opendatahub.io/port` on the ExternalProvider when the remote gateway listens elsewhere |
| TLS | Always on. `auth` is required and the controller rejects plaintext transport with `apikey` auth (CWE-319) |
| Auth header | `Authorization: Bearer <cluster-b MaaS API key>` |
| API path | `/v1/chat/completions` (set in `path` on the ExternalModel provider ref; there is no `path` on ExternalProvider) |
| `apiFormat` | `openai-chat` |
| Request format | OpenAI Chat Completions (pass-through) |
| Response format | OpenAI Chat Completions (pass-through) |
| `targetModel` | The model name Cluster B serves it under (its ExternalModel `spec.modelName`, or the local model's name) |

## Setup

### Step 1: Mint a MaaS API key on Cluster B

Log in to Cluster B as a user who is allowed to call the target model and
create a key for Cluster A. The key inherits that user's MaaSAuthPolicy and
MaaSSubscription, so the subscription you pass sets the rate limit that
Cluster B will apply to all traffic arriving from Cluster A.

```bash
# on Cluster B
GW_HOST_B=$(kubectl get gateway maas-default-gateway -n openshift-ingress \
  -o jsonpath='{.spec.listeners[0].hostname}')
GW_PORT_B=${GW_PORT_B:-443}   # the HTTPS listener port; anything other than 443 also goes in the port annotation in step 3
TOKEN=$(oc whoami -t)

KEY_B=$(curl -sS -X POST "https://${GW_HOST_B}:${GW_PORT_B}/maas-api/v1/api-keys" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name":"cluster-a-federation","subscription":"<subscription-name>"}' | jq -r '.key')
```

Confirm the key works directly against Cluster B before involving Cluster A:

```bash
curl -sS "https://${GW_HOST_B}:${GW_PORT_B}/v1/chat/completions" \
  -H "Authorization: Bearer ${KEY_B}" \
  -H "Content-Type: application/json" \
  -d '{"model":"<model-name-on-b>","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'
```

Keep `GW_HOST_B`, `GW_PORT_B` and `<model-name-on-b>`: they become
`endpoint`, the port annotation and `targetModel` on Cluster A.

### Step 2: Store the key as a Secret on Cluster A

The Secret lives in the same namespace as the ExternalProvider that references
it and **must** carry the `inference.llm-d.ai/ipp-managed: "true"` label, or
the credential store ignores it. The manifest is piped over stdin so the key
never appears in a process argument list.

```bash
# on Cluster A
kubectl apply -n llm -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: cluster-b-credentials
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: ${KEY_B}
EOF
```

### Step 3: Deploy the ExternalProvider on Cluster A

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: cluster-b
  namespace: llm
  # Only needed when GW_PORT_B from step 1 is not 443:
  # annotations:
  #   inference.opendatahub.io/port: "8443"
spec:
  provider: remote-maas
  endpoint: maas.apps.cluster-b.example.com   # GW_HOST_B from step 1
  auth:
    type: apikey
    secretRef:
      name: cluster-b-credentials
```

The controller creates a ServiceEntry, a DestinationRule with TLS mode
`SIMPLE`, and an ExternalName Service for the endpoint. Do **not** set
`inference.opendatahub.io/tls: "false"`: plaintext with `apikey` auth is
rejected.

### Step 4: Deploy the ExternalModel(s) on Cluster A

One ExternalModel per remote model you want to expose. `path` is required on
the provider ref and is the path Cluster B's gateway serves inference on.

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: llama-on-b
  namespace: llm
spec:
  modelName: llama-on-b          # name clients send to Cluster A; defaults to metadata.name
  externalProviderRefs:
    - ref:
        name: cluster-b
      targetModel: llama-3-70b   # name Cluster B serves the model under
      apiFormat: openai-chat
      path: /v1/chat/completions
```

The model name in the request body is rewritten to `targetModel` before the
hop, so the client-facing name on A and the name on B can differ.

### Step 5: Set `CLUSTER_NAME` on both clusters

Loop detection is disabled when `CLUSTER_NAME` is unset, and a request that
bounces A → B → A would then be forwarded until something else times it out.
Give each cluster a distinct name through the payload-processing Helm release
(see the install step in the [README](../../README.md)):

```bash
# Cluster A
helm upgrade payload-processing ./deploy/payload-processing --namespace openshift-ingress --reuse-values \
  --set upstreamIpp.payloadProcessor.env[2].name=CLUSTER_NAME \
  --set upstreamIpp.payloadProcessor.env[2].value=cluster-a

# Cluster B
helm upgrade payload-processing ./deploy/payload-processing --namespace openshift-ingress --reuse-values \
  --set upstreamIpp.payloadProcessor.env[2].name=CLUSTER_NAME \
  --set upstreamIpp.payloadProcessor.env[2].value=cluster-b
```

Verify on each cluster:

```bash
kubectl set env deploy -n openshift-ingress --list --all | grep CLUSTER_NAME
```

The comparison is case-insensitive. The header is not authenticated: the
headers guard records whatever `X-Origin-Cluster` value arrives, strips it
from the request, and the resolver re-adds it on the next `remote-maas` hop.
A client that sets the header only affects its own request (a forged value
can make a hop reject it with 403), and the header is never forwarded to
third-party providers, but the gateway does not verify that the value came
from a trusted cluster.

### Step 6: Verify with a test request

The client needs an ordinary MaaS key for Cluster A, issued the same way as
in step 1 but on Cluster A, for a user whose MaaSAuthPolicy and
MaaSSubscription cover the `llama-on-b` model:

```bash
# on Cluster A
GW_HOST_A=$(kubectl get gateway maas-default-gateway -n openshift-ingress \
  -o jsonpath='{.spec.listeners[0].hostname}')
TOKEN=$(oc whoami -t)

KEY_A=$(curl -sS -X POST "https://${GW_HOST_A}/maas-api/v1/api-keys" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name":"llama-on-b-test","subscription":"<subscription-name-on-a>"}' | jq -r '.key')
```

Then check the generated resources and send a request:

```bash
kubectl get httproute llama-on-b -n llm                 # created by the ExternalModel controller
kubectl get serviceentry,destinationrule cluster-b -n llm

curl -sS "https://${GW_HOST_A}/v1/chat/completions" \
  -H "Authorization: Bearer ${KEY_A}" \
  -H "Content-Type: application/json" \
  -d '{"model":"llama-on-b","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'
```

Cluster A authenticates and rate-limits `KEY_A`; Cluster B authenticates and
rate-limits the key from step 1. Both limits apply.

## Complete manifest

A copy-paste version of steps 2 to 4 is in
[`deploy/samples/manifests/remote-maas.yaml`](../../deploy/samples/manifests/remote-maas.yaml).

## What crosses the cluster boundary

| Header | Behavior |
|--------|----------|
| `Authorization` | Replaced with Cluster B's key from the Secret |
| `X-Origin-Cluster` | Set to the origin cluster's `CLUSTER_NAME` on `remote-maas` hops; preserved on further hops; never sent to third-party providers |
| `x-gateway-model-name`, `x-ipp-selected-provider` | Stripped on arrival at Cluster B so it resolves the model itself |
| `x-b3-traceid`, `x-request-id` | Forwarded, so a trace ID can be followed across both clusters' logs |

## Troubleshooting

Errors from Cluster B are returned to the client by Cluster A, so first work
out which cluster produced the error. Cluster A's payload-processing log shows
the resolved provider and the outgoing hop; Cluster B's shows whether the
request arrived and how it resolved there.

| Symptom | Cause | Fix |
|---------|-------|-----|
| `404` from Cluster B | `path` on the ExternalModel ref does not match the path Cluster B's gateway serves, or `targetModel` is not a model name Cluster B knows | Run the direct curl from step 1 against Cluster B with the same path and model; set `path` and `targetModel` to the values that work there |
| `401` or `403` from Cluster B without the loop message | Key from step 1 expired, revoked, or its user lost access to the model on B | Mint a new key on Cluster B and update the Secret on A. The credential store picks up the change without a restart |
| `500 ... authType 'apikey' credentials not found` | Secret missing the `inference.llm-d.ai/ipp-managed: "true"` label, or in a different namespace than the ExternalProvider | `kubectl label secret <name> -n <ns> inference.llm-d.ai/ipp-managed=true` |
| `403 ... routing loop detected: request originated from this cluster` | An ExternalModel on Cluster B points back at Cluster A for the same model, so the request returned to its origin | Fix the ExternalModel on B. This is the detector working as designed, not a client error. If you see a real loop but no 403, `CLUSTER_NAME` is unset on the cluster that should have rejected it |
| `503` with a DNS or `no healthy upstream` message in Envoy | `endpoint` is not resolvable from Cluster A's gateway proxy, or the ServiceEntry was not created | `kubectl get serviceentry -n <ns>`; `endpoint` must be a DNS hostname, not an IP, and the gateway namespace must be allowed egress to it |
| `503` with a TLS handshake or certificate error in Envoy | Cluster B's gateway certificate does not chain to a CA trusted by Cluster A's gateway proxy, or the hostname in `endpoint` is not covered by the certificate | Use a publicly trusted or organization-trusted certificate on Cluster B's gateway. Custom CA bundles and skip-verify are tracked in RHAIRFE-2243 / RHAIRFE-2242 |
| `insecure transport configuration: plaintext transport (TLS disabled) is not allowed with auth type "apikey"` | `inference.opendatahub.io/tls: "false"` on a `remote-maas` provider | Remove the annotation. Cross-cluster hops always use TLS |
| Request succeeds but reaches the wrong model on B | `targetModel` is Cluster B's `metadata.name` instead of its `spec.modelName` | Cluster B matches on the client-facing name. Use B's `spec.modelName` (or `metadata.name` only when `spec.modelName` is unset) |

Note that loop detection catches a request that comes back to the cluster it
started from. A cycle between two *other* clusters downstream of the origin
(A → B → C → B) is not rejected, because B sees origin `cluster-a`, not its
own name.

## Testing locally

The repository ships a two-Kind-cluster E2E for this provider:

```bash
make test-e2e-cross-cluster-setup   # two Kind clusters, throwaway CA, echo backend
make test-e2e-cross-cluster         # ginkgo label cross-cluster
make test-e2e-cross-cluster-teardown
```

The manifests it applies (`test/e2e/crosscluster/suite_test.go`) are the
reference for a working `remote-maas` configuration.

## Related

- [ExternalProvider and ExternalModel CRDs](../external-provider-model.md)
- [Hub mode](../../deploy/examples/hub-mode/README.md) is a different mechanism where an EPP hub picks a spoke cluster per request. This guide covers the static, admin-configured mapping of a model to one remote cluster.
