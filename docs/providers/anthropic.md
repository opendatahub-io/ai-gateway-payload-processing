# Anthropic Provider (`anthropic`)

## Overview

The `anthropic` provider translates OpenAI Chat Completions requests to Anthropic's Messages API
format and translates responses back. This includes system message extraction, tool calling
translation, and usage field mapping.

## Configuration

| Field | Value |
|-------|-------|
| Provider type | `anthropic` |
| Endpoint | `api.anthropic.com` |
| Auth header | `x-api-key: <API_KEY>` (NOT Bearer token) |
| API path | `/v1/messages` |
| `apiFormat` | `messages` |
| Request format | Translated from OpenAI to Anthropic Messages API |
| Response format | Translated from Anthropic back to OpenAI format |

## What Gets Translated

**Request:**
- System messages extracted to top-level `system` field
- `developer` role mapped to system
- `max_tokens` / `max_completion_tokens` forwarded
- `tools[]` converted to Anthropic format with `input_schema`
- `tool_choice` mapped: `auto` → `{"type":"auto"}`, `required` → `{"type":"any"}`
- `tool` role messages converted to `tool_result` content blocks
- `stream` field forwarded
- `anthropic-version: 2023-06-01` header added

**Response:**
- `stop_reason` mapped: `end_turn` → `stop`, `max_tokens` → `length`, `tool_use` → `tool_calls`
- `input_tokens/output_tokens` → `prompt_tokens/completion_tokens`
- Tool use blocks converted to OpenAI `tool_calls` format
- Error responses translated to OpenAI error format

## Example

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
  api-key: "sk-ant-..."
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

## How to Get an API Key

1. Go to https://console.anthropic.com/settings/keys
2. Create a new API key
3. Copy the key (starts with `sk-ant-`)

## Supported Models

- `claude-sonnet-4-20250514`
- `claude-haiku-4-5-20251001`
- `claude-opus-4-20250514`

Full list: https://docs.anthropic.com/en/docs/about-claude/models

## Known Limitations

- `frequency_penalty`, `presence_penalty`, `logprobs`, `n`, `response_format`, `seed` are silently
  dropped (Anthropic doesn't support these parameters)
- `anthropic-beta` header passes through unchanged — Claude Code Tool Search (`anthropic-beta: tool-search-tool-2025-10-19`) works natively through the gateway

## Testing

```bash
# Direct API test (native Anthropic format)
curl -sk "https://api.anthropic.com/v1/messages" \
  -H "x-api-key: <YOUR_API_KEY>" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet-4-20250514","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'

# Through MaaS gateway (OpenAI format — translated automatically)
curl -sk "https://${GATEWAY_HOST}/v1/chat/completions" \
  -H "Authorization: Bearer ${MAAS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'
```

## Official Documentation

- API Reference: https://docs.anthropic.com/en/api/messages
- Models: https://docs.anthropic.com/en/docs/about-claude/models
- Tool Use: https://docs.anthropic.com/en/docs/build-with-claude/tool-use
