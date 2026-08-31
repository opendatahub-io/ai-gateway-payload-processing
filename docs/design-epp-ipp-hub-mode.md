# Design: EPP↔IPP Coordination for Hub Mode Multi-Cluster Routing

## Status

Draft — July 2026

## Author

Inference Gateway Team

## Problem Statement

In hub-and-spoke multi-cluster LLM inference, a hub cluster routes requests to spoke clusters based on real-time metrics (KV-cache locality, load, cost). The EPP (Endpoint Picker) hub owns the routing decision. The IPP (Inference Payload Processor) must transform the request for whichever spoke the EPP selects — but it currently both selects AND transforms in a single step via weighted random. These concerns must be separated.

## Filter Chain

### Standard mode (single cluster)

```
Client → IPP-Pre → Auth → IPP-Post → Backend
                          └─ selectByWeight → transform
```

One IPP call handles everything: pick a provider by weight, set routing headers, transform the request.

### Hub mode (multi-cluster)

```
Client → IPP-Pre [PROPOSE] → Auth → EPP Hub [PICK] → IPP-Post [TRANSFORM] → Backend
```

The IPP runs twice, as two separate deployments: `ipp-pre` (before auth/EPP) proposes candidates, and `ipp-post` (after the EPP) transforms for the chosen destination. Each deployment runs `model-provider-resolver` with `hubMode: true`; the plugin auto-detects its phase by checking for the `x-gateway-destination-endpoint` header — absent means PROPOSE, present means TRANSFORM.

## Contract

### Phase 1: IPP → EPP (PROPOSE)

The IPP tells the EPP which spoke endpoints are eligible for this model.

| Field | Value |
|-------|-------|
| **Transport** | `ProcessingResponse.DynamicMetadata` (Envoy ext-proc field 8) |
| **Namespace** | `envoy.lb.subset_hint` |
| **Key** | `x-gateway-destination-endpoint-subset` |
| **Value** | `ListValue` of string endpoints (e.g. `["maas.spoke-east.example.com", "maas.spoke-west.example.com"]`) |
| **When set** | Only for models with eligible ExternalProviderRefs (weight > 0) |

The IPP does NOT select a provider, set routing headers, or write CycleState during PROPOSE.

**Implementation:** `model-provider-resolver` calls `dynamicmetadata.SetEndpointSubset()` which sets a pseudo-header. The gRPC stream wrapper (`dynamicmetadata.WrapServer`) intercepts `Send()`, strips the pseudo-header, and populates `ProcessingResponse.DynamicMetadata` before Envoy sees it.

### Phase 2: EPP → IPP (PICK)

The EPP selects a spoke and tells the IPP which one was chosen.

| Field | Value |
|-------|-------|
| **Transport** | Request header (set by EPP on the forwarded request) |
| **Header** | `x-gateway-destination-endpoint` |
| **Value** | Single endpoint string (e.g. `"maas.spoke-west.example.com:443"`) |
| **Set by** | EPP after its Filter → Score → Pick pipeline |

The EPP uses the subset hint from Phase 1 to narrow candidates before scoring.

### Phase 3: IPP (TRANSFORM)

The IPP reads the EPP's destination pick and transforms the request.

| Step | Action |
|------|--------|
| Read destination | `request.Headers["x-gateway-destination-endpoint"]` |
| Match to provider | `findRefByEndpoint()` — strips port, matches hostname against `ExternalProvider.spec.endpoint`, skipping disabled refs (weight ≤ 0) |
| Set routing header | `x-ipp-selected-provider` = matched ExternalProvider name |
| Set Host | `Host` = matched endpoint |
| Rewrite model | Body `model` field → `ExternalProviderRef.targetModel` |
| Write CycleState | Provider, model, API format, auth, endpoint, path, credentials |

Downstream plugins (`api-translation`, `apikey-injection`) consume CycleState identically to standard mode.

## Example CRDs

### ExternalProvider (one per spoke cluster)

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: spoke-east
  namespace: hub-gateway
spec:
  provider: openai                              # spoke exposes OpenAI-compatible API
  endpoint: maas.spoke-east.example.com         # spoke MaaS gateway FQDN
  auth:
    type: apikey
    secretRef:
      name: spoke-east-api-key
```

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: spoke-west
  namespace: hub-gateway
spec:
  provider: openai
  endpoint: maas.spoke-west.example.com
  auth:
    type: apikey
    secretRef:
      name: spoke-west-api-key
```

### ExternalModel (one per model, refs all eligible spokes)

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: llama-4-scout
  namespace: hub-gateway
spec:
  modelName: llama-4-scout
  externalProviderRefs:
  - ref:
      name: spoke-east
    targetModel: llama-4-scout        # model name on the spoke
    apiFormat: openai-chat
    path: /v1/chat/completions
    weight: 1                         # eligible for routing
  - ref:
      name: spoke-west
    targetModel: llama-4-scout
    apiFormat: openai-chat
    path: /v1/chat/completions
    weight: 1
```

### Plugin config (model-provider-resolver)

```yaml
- type: model-provider-resolver
  parameters:
    hubMode: true
```

## Request Lifecycle

```
1. Client sends: POST /v1/chat/completions
   Body: {"model": "llama-4-scout", "messages": [...]}

2. IPP-Pre [PROPOSE]:
   - body-field-to-header: sets X-Gateway-Model-Name: llama-4-scout
   - model-provider-resolver (hubMode, no destination header):
     → resolves "llama-4-scout" → finds 2 refs (spoke-east, spoke-west)
     → sets DynamicMetadata:
       envoy.lb.subset_hint:
         x-gateway-destination-endpoint-subset:
           ["maas.spoke-east.example.com", "maas.spoke-west.example.com"]
     → returns (no CycleState, no routing headers)

3. Auth (WasmPlugin): validates API key / token

4. EPP Hub [PICK]:
   - Reads subset hint from request metadata
   - Filters candidates to spoke-east and spoke-west
   - Scores by KV-cache hit rate, load, latency
   - Picks spoke-west (best score)
   - Sets header: x-gateway-destination-endpoint: maas.spoke-west.example.com:443

5. IPP-Post [TRANSFORM]:
   - model-provider-resolver (hubMode, destination header present):
     → reads "maas.spoke-west.example.com:443"
     → strips port → matches "maas.spoke-west.example.com" to spoke-west ref
     → sets x-ipp-selected-provider: spoke-west
     → sets Host: maas.spoke-west.example.com
     → writes CycleState (provider=openai, model=llama-4-scout, auth=apikey, ...)
   - api-translation: reads CycleState, translates if needed
   - apikey-injection: reads CycleState, injects spoke-west credentials

6. Envoy routes to maas.spoke-west.example.com:443
```

## Edge Cases

| Scenario | Behavior |
|----------|----------|
| Model not found in store | Pass-through (nil error). No metadata set, no CycleState. Internal models handled normally. |
| No eligible spokes (all weight=0) | PROPOSE returns nil. No metadata set. EPP receives no subset hint. |
| Destination doesn't match any ref | TRANSFORM returns `BadRequest` error: "no ExternalProvider matches destination". |
| Destination matches a disabled ref (weight=0) | Treated as no match — `findRefByEndpoint` skips weight≤0 refs, so a stale or spoofed destination can't route to a provider PROPOSE never advertised. Returns the same `BadRequest`. |
| Destination has port, ref doesn't | Port stripped before matching. `maas.spoke.com:443` matches `maas.spoke.com`. |
| Hub mode disabled | Standard `selectByWeight` behavior. No phase detection. |

## Deployment

Hub mode is deployed as two IPP instances of the `deploy/payload-processing` Helm chart, ordered around the auth and EPP ext-proc filters via generated Istio EnvoyFilters:

| Instance | Runs | Plugin chain | EnvoyFilter ordering |
|----------|------|--------------|----------------------|
| `ipp-pre` | Before auth/EPP (PROPOSE) | `body-field-to-header` → `model-provider-resolver` (hubMode) | `INSERT_BEFORE` the auth filter |
| `ipp-post` | After the EPP (TRANSFORM) | `model-provider-resolver` (hubMode) → `stream-usage-enforcer` → `api-translation` → `apikey-injection` | `INSERT_AFTER` the EPP ext-proc filter |

The `provider.istio.envoyFilter.operation` / `anchorSubFilter` values in `deploy/payload-processing/values.yaml` control where each instance's ext-proc filter is spliced into the chain. Ready-to-use overlays live in `deploy/examples/hub-mode/`:

- `values-hub-pre.yaml` — the PROPOSE instance
- `values-hub-post.yaml` — the TRANSFORM instance
- `README.md` — install steps and how to set `anchorSubFilter` to your auth and EPP filter names

## Related Issues

- #408 — Dynamic metadata support in ext-proc response (pseudo-header → DynamicMetadata)
- #409 — Hub mode for model-provider-resolver (PROPOSE/TRANSFORM logic)
- #410 — EnvoyFilter ordering for hub mode (IPP-Post after EPP)
- #276 — Parent: cross-cluster inference routing via ExternalModel/Provider
