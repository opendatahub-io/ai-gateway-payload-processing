# OpenAI Provider (`openai`)

## Overview

The `openai` provider routes requests to the OpenAI API. Since the gateway uses OpenAI Chat Completions
as the canonical request format, this translator only rewrites the `:path` header — no body mutation is needed.

## Configuration

| Field | Value |
|-------|-------|
| Provider type | `openai` |
| Endpoint | `api.openai.com` |
| Auth header | `Authorization: Bearer <API_KEY>` |
| API path | `/v1/chat/completions` |
| `apiFormat` | `openai-chat` |
| Request format | OpenAI Chat Completions (pass-through) |
| Response format | OpenAI Chat Completions (pass-through) |

## Example

```yaml
---
apiVersion: v1
kind: Secret
metadata:
  name: openai-key
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: "sk-proj-..."
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: openai-prod
  namespace: llm
spec:
  provider: openai
  endpoint: api.openai.com
  auth:
    type: apikey
    secretRef:
      name: openai-key
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: gpt4o
  namespace: llm
spec:
  modelName: gpt-4o
  externalProviderRefs:
    - ref:
        name: openai-prod
      targetModel: gpt-4o
      apiFormat: openai-chat
      path: /v1/chat/completions
```

## How to Get an API Key

1. Go to https://platform.openai.com/api-keys
2. Create a new API key
3. Copy the key (starts with `sk-proj-`)

## Supported Models

Any model available on the OpenAI API, including:
- `gpt-4o`, `gpt-4o-mini`
- `gpt-4.1`, `gpt-4.1-mini`, `gpt-4.1-nano`
- `o1`, `o3`, `o3-mini`, `o4-mini`

Full list: https://platform.openai.com/docs/models

## Testing

```bash
# Direct API test (no gateway)
curl -sk "https://api.openai.com/v1/chat/completions" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'

# Through MaaS gateway
curl -sk "https://${GATEWAY_HOST}/v1/chat/completions" \
  -H "Authorization: Bearer ${MAAS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'
```

## Official Documentation

- API Reference: https://platform.openai.com/docs/api-reference/chat/create
- Models: https://platform.openai.com/docs/models
- Rate Limits: https://platform.openai.com/docs/guides/rate-limits
