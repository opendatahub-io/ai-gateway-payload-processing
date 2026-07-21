# Hub Mode Deployment

Hub mode enables cross-cluster inference routing where an EPP (Endpoint Picker) hub selects which spoke cluster handles each request. The IPP operates in two phases, deployed as two separate Helm releases:

```
IPP-Pre [PROPOSE] → Auth → EPP Hub [picks spoke] → IPP-Post [TRANSFORM] → Backend
```

## Filter chain

| Deployment | Phase | EnvoyFilter anchor | Purpose |
|---|---|---|---|
| `ipp-pre` | PROPOSE | `INSERT_BEFORE` auth | Extract model, set endpoint subset metadata for EPP |
| `ipp-post` | TRANSFORM | `INSERT_AFTER` EPP | Match EPP destination to provider, apply transformation |

## Deployment

```bash
# Pre-processing: runs before auth and EPP
helm install ipp-pre ./deploy/payload-processing \
  -f deploy/examples/hub-mode/values-hub-pre.yaml \
  --set upstreamIpp.provider.istio.envoyFilter.anchorSubFilter=<AUTH_FILTER_NAME>

# Post-processing: runs after EPP
helm install ipp-post ./deploy/payload-processing \
  -f deploy/examples/hub-mode/values-hub-post.yaml \
  --set upstreamIpp.provider.istio.envoyFilter.anchorSubFilter=<EPP_FILTER_NAME>
```

Replace `<AUTH_FILTER_NAME>` with your auth filter (e.g. the WasmPlugin CR filter name) and `<EPP_FILTER_NAME>` with the EPP ext-proc filter (e.g. `envoy.filters.http.ext_proc.epp-hub`).

## How it works

The `model-provider-resolver` plugin auto-detects which phase it's in by checking for the `x-gateway-destination-endpoint` header:

- **PROPOSE** (header absent): Resolves model name, collects eligible spoke endpoints from ExternalProvider CRDs, and sets `envoy.lb.subset_hint` dynamic metadata so the EPP can filter candidates.
- **TRANSFORM** (header present): Reads the EPP's destination pick, matches it to the correct ExternalProvider, and writes CycleState so downstream plugins (`api-translation`, `apikey-injection`) apply the right transformation.
