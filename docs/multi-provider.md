# Multi-Provider Traffic Splitting

An ExternalModel can reference multiple ExternalProviders and distribute traffic across them
using weighted random selection.

## How it works

Each entry in `externalProviderRefs` has an optional `weight` field (1–100, default: 1).
On each request, the gateway selects one provider ref using weighted random selection.
Higher weight means proportionally more traffic.

Setting `weight: 0` disables a provider ref without removing it from the CR — useful for
temporarily taking a provider out of rotation.

## Example: Anthropic direct + Vertex AI fallback

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: claude-ha
  namespace: llm
spec:
  modelName: claude-sonnet
  externalProviderRefs:
    - ref:
        name: anthropic-prod    # 80% of traffic
      targetModel: claude-sonnet-4-20250514
      apiFormat: messages
      path: /v1/messages
      weight: 80
    - ref:
        name: vertex-gcp        # 20% of traffic
      targetModel: claude-sonnet-4-20250514
      apiFormat: vertex-messages
      path: /v1/projects/{project}/locations/{location}/publishers/anthropic/models/{model}:rawPredict
      weight: 20
```

Each provider ref can have a different:
- `apiFormat` — different translation applied per provider
- `path` — different URL structure
- `config` — model-level config overrides provider config for this binding
- `auth` — per-binding credential override

## How traffic is distributed

| Scenario | Behavior |
|----------|---------|
| Single ref | That provider always selected (no randomness) |
| Multiple refs, equal weight | Uniform distribution |
| Multiple refs, unequal weight | Proportional to weight values |
| All refs `weight: 0` | Request fails with `all providers disabled` |
| One ref `weight: 0` | Effectively removed from selection |

## Credential rotation without downtime

Because providers are separate CRs from models, credential rotation is a single operation:

```bash
# Update the Secret — picked up automatically within seconds, no restart needed
kubectl create secret generic anthropic-key \
  --from-literal=api-key=sk-ant-NEW-KEY \
  --dry-run=client -o yaml \
  | kubectl apply -f -
```

No ExternalModel or ExternalProvider changes required. No pod restart.

## Provider-level auth override

Individual provider refs can override the ExternalProvider's auth:

```yaml
externalProviderRefs:
  - ref:
      name: shared-openai-provider
    targetModel: gpt-4o
    apiFormat: openai-chat
    path: /v1/chat/completions
    auth:
      type: apikey
      secretRef:
        name: team-b-openai-key   # different key for this model binding
```

## Config inheritance and override

The merged config (for path placeholder resolution) applies model config on top of provider config.
The same key in model config takes precedence:

```yaml
# Provider config:
config:
  project: default-project     # used unless overridden
  location: us-central1

# Model ref config — overrides provider for this binding only:
config:
  project: special-project     # wins over provider's "default-project"
```
