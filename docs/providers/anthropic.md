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

## ExternalModel Example

```yaml
apiVersion: maas.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: my-anthropic-model
  namespace: llm
spec:
  provider: anthropic
  targetModel: claude-haiku-4-5-20251001
  endpoint: api.anthropic.com
  credentialRef:
    name: anthropic-api-key
```

## Secret Example

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: anthropic-api-key
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: "sk-ant-api03-..."
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
- Multimodal content (images) is extracted as text-only; full multimodal support is tracked separately

## Testing

```bash
# Direct API test (native Anthropic format)
curl -sk "https://api.anthropic.com/v1/messages" \
  -H "x-api-key: <YOUR_API_KEY>" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-haiku-4-5-20251001","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'

# Through MaaS gateway (OpenAI format — translated automatically)
curl -sk "https://${GATEWAY_HOST}/llm/my-anthropic-model/v1/chat/completions" \
  -H "Authorization: Bearer ${MAAS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-haiku-4-5-20251001","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'
```

## Official Documentation

- API Reference: https://docs.anthropic.com/en/api/messages
- Models: https://docs.anthropic.com/en/docs/about-claude/models
- Tool Use: https://docs.anthropic.com/en/docs/build-with-claude/tool-use
