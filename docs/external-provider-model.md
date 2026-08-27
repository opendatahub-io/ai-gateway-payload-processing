# ExternalProvider and ExternalModel CRDs

This document describes the two-resource CRD architecture introduced for multi-provider
external model serving.

## Overview

The platform exposes two custom resources:

| Resource | Purpose |
|----------|---------|
| `ExternalProvider` | Provider account — endpoint, auth credentials, shared config |
| `ExternalModel` | Client-facing model — maps a model name to one or more providers |

This separation allows:
- **Credential rotation** as a single operation (update the ExternalProvider Secret)
- **Multi-provider traffic splitting** — one model served by multiple providers with weighted selection
- **Explicit API format selection** — declare which translation the gateway applies
- **Path placeholder substitution** — parameterize provider-specific URL paths via config

## ExternalProvider

An ExternalProvider represents a single provider account or endpoint. Multiple ExternalModels
can reference the same ExternalProvider.

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: my-anthropic
  namespace: llm
spec:
  provider: anthropic          # freeform label — no enum validation, used for logging
                               # and auth header defaults (e.g. anthropic → x-api-key)
  endpoint: api.anthropic.com  # must be a DNS hostname, not a bare IP
  auth:
    type: apikey               # apikey | sigv4 | oauth2 | none
    secretRef:
      name: anthropic-key      # K8s Secret in the same namespace
  config:                      # optional key-value pairs for path placeholders
    # example: project: my-gcp-project
```

### Auth types

| Type | Description | Secret fields |
|------|-------------|--------------|
| `apikey` | HTTP header auth (API key) | `api-key` |
| `sigv4` | AWS SigV4 request signing | `aws-access-key-id`, `aws-secret-access-key`, `aws-session-token` (optional) |
| `oauth2` | GCP service account → OAuth2 Bearer token | `gcp-service-account-json` |
| `none` | Strip client auth, inject nothing (mTLS routes) | — |

### Credential Secret

The Secret **must** carry the label `inference.llm-d.ai/ipp-managed: "true"` or the
credential store will ignore it and every request fails with `authType credentials not found`.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: anthropic-key
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: sk-ant-...
```

### Status

```bash
kubectl get externalproviders my-anthropic -n llm -o jsonpath='{.status.phase}'
# Ready | Failed | Pending
```

## ExternalModel

An ExternalModel defines the client-facing model name and binds it to one or more
ExternalProviders. Clients use the model name in their inference requests; the gateway
resolves the provider and performs translation transparently.

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: claude
  namespace: llm
spec:
  # modelName: claude-sonnet  # coming in a future release — defaults to metadata.name
                              # use when the desired name isn't a valid k8s name
  externalProviderRefs:
    - ref:
        name: my-anthropic    # ExternalProvider in the same namespace
      targetModel: claude-sonnet-4-20250514   # provider-side model name
      apiFormat: messages     # openai-chat | messages | vertex-messages
      path: /v1/messages      # outgoing :path — supports {key} placeholders
      weight: 1               # optional, 1–100 (default: 1)
      config: {}              # optional, overrides provider config for this binding
      auth: {}                # optional, overrides provider auth for this binding
```

### `spec.modelName` (upcoming)

A future release will add `spec.modelName` to decouple the client-facing model name from
`metadata.name`. This is useful when the desired name contains characters not allowed in
Kubernetes resource names (dots, uppercase letters, colons, slashes). Until then, clients
use `metadata.name` as the model name.

### `apiFormat`

Controls which translator the gateway applies:

| `apiFormat` | Input format | Translation |
|-------------|-------------|-------------|
| `openai-chat` | OpenAI Chat Completions | Path rewrite only (same format) |
| `messages` | Anthropic Messages API | OpenAI → Anthropic body conversion |
| `vertex-messages` | Vertex AI Anthropic | OpenAI → Anthropic + Vertex-specific adjustments |

> **The OpenAI Responses API (`/v1/responses`) is not supported for external models.**
> No translator is registered for it, so a request routed to it fails with
> `unsupported format combination`. Use `openai-chat` (Chat Completions) instead.
> These three values are the only supported `apiFormat` inputs today.

### `path` and config placeholders

The `path` field sets the outgoing `:path` pseudo-header. It supports `{key}` placeholders
that are resolved from the merged config (provider config + model config override):

```yaml
# Provider config:
config:
  project: my-gcp-project
  location: us-central1

# Model path:
path: /v1/projects/{project}/locations/{location}/publishers/anthropic/models/{model}:rawPredict
#                  ^^^^^^^^             ^^^^^^^^^^                                ^^^^^^^ 
# {project} and {location} from config, {model} is reserved → resolves to targetModel
```

The `{model}` placeholder is reserved and auto-resolves to `targetModel` without requiring
a config entry. An explicit `model` key in config takes precedence.

If a placeholder cannot be resolved, the ExternalModel status is set to `Failed` with a
message listing the missing config keys.

### `weight`

Used for weighted random traffic splitting when multiple provider refs are defined.
See [Multi-provider traffic splitting](multi-provider.md) for details.

### Status

```bash
kubectl get externalmodel claude -n llm -o jsonpath='{.status}'
# {"phase":"Ready","httpRouteName":"claude","conditions":[...]}
```

The `httpRouteName` field identifies the HTTPRoute created by the controller, which
downstream policy controllers (e.g., maas-controller) can use to attach auth/rate-limit
policies without assuming naming conventions.

## How the gateway processes a request

1. Client sends a chat completions request with `"model": "claude-sonnet"` — either via the path-routed URL (`/{namespace}/{modelName}/v1/chat/completions`) or body-routed (`/v1/chat/completions` with model in the request body, which is the default for ExternalModel routing)
2. `body-field-to-header` extracts model name → `X-Gateway-Model-Name: claude-sonnet`
3. `model-provider-resolver` looks up `claude-sonnet` → resolves ExternalProvider, selects
   a provider ref (weighted random if multiple), writes provider info to CycleState
4. `api-translation` reads `(inputFormat, outputFormat)` from CycleState, translates the
   request body and path
5. `apikey-injection` swaps the client's MaaS API key for the provider credential
6. Gateway forwards to the provider's endpoint
7. Response is translated back and returned to the client

## Complete example

### Direct Anthropic

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: anthropic-key
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: sk-ant-...
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: anthropic-prod
  namespace: llm
spec:
  provider: anthropic
  endpoint: api.anthropic.com
  auth:
    type: apikey
    secretRef:
      name: anthropic-key
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: claude
  namespace: llm
spec:
  modelName: claude-sonnet
  externalProviderRefs:
    - ref:
        name: anthropic-prod
      targetModel: claude-sonnet-4-20250514
      apiFormat: messages
      path: /v1/messages
```

### Vertex AI (Gemini, OpenAI-compatible)

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: gcp-sa
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  gcp-service-account-json: |
    {"type":"service_account","project_id":"my-project",...}
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: vertex-gcp
  namespace: llm
spec:
  provider: vertex-openai
  endpoint: us-central1-aiplatform.googleapis.com
  auth:
    type: oauth2
    secretRef:
      name: gcp-sa
  config:
    project: my-gcp-project
    location: us-central1
    endpoint: openapi
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: gemini
  namespace: llm
spec:
  externalProviderRefs:
    - ref:
        name: vertex-gcp
      targetModel: gemini-2.0-flash
      apiFormat: openai-chat
      path: /v1/projects/{project}/locations/{location}/endpoints/{endpoint}/chat/completions
```

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| `ExternalModel status: Failed — ExternalProvider not found` | Provider CR missing or wrong namespace | Create ExternalProvider in same namespace |
| `ExternalModel status: Failed — ExternalProvider not ready` | Provider controller not running or provider CRD not installed | Check BBR pod logs; ensure CRDs installed |
| `authType credentials not found` | Secret missing `ipp-managed` label | `kubectl label secret <name> inference.llm-d.ai/ipp-managed=true` |
| `path has unresolved placeholders [location]` | Config key missing from provider and model | Add `location: <value>` to ExternalProvider config |
| `unsupported format combination: openai-chat → azure-openai` | Wrong `apiFormat` value | Use `openai-chat` for Azure OpenAI, not `azure-openai` |
