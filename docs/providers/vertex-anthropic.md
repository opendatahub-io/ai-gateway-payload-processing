# Vertex AI — Anthropic (Claude)

Routes Anthropic Claude models through Google Cloud Vertex AI's publisher
endpoint (`publishers/anthropic/models/{model}:streamRawPredict`).

Two input formats are supported:

| Client sends | Translator | Registration |
|---|---|---|
| OpenAI Chat Completions (`/v1/chat/completions`) | `VertexAnthropicTranslator` | `{openai-chat, vertex-messages}` |
| Anthropic Messages (`/v1/messages`, e.g. Claude Code) | `VertexAnthropicPassthroughTranslator` | `{messages, vertex-messages}` |

Authentication uses the GCP OAuth2 generator (`auth.type: oauth2`) with a
Service Account JSON — see the apikey-injection plugin docs.

## Required config

`ExternalModel`/`ExternalProvider` `spec.config` must include:

| Key | Example | Notes |
|---|---|---|
| `anthropicVersion` | `vertex-2023-10-16` | Injected into the request body (Vertex carries the API version in the body, not a header) |
| `project`, `location` | — | Consumed by path `{placeholders}` |

The `model` field is removed from the body — Vertex carries the model in the
URL path (`ExternalProviderRef.path`).

## Limitations: dropped Anthropic features

Vertex rejects requests containing fields it does not recognize
(`400 "Extra inputs are not permitted"`), so the translators **silently strip**
the following before forwarding. Clients using these features lose them on
Vertex-routed models with no error:

| Stripped | Effect |
|---|---|
| `anthropic-beta` header | All beta features (e.g. fine-grained tool streaming, context management betas) are disabled |
| `context_management` | Server-side context editing/compaction is not applied |
| `mcp_servers` | MCP connector servers are not attached |
| `service_tier` | Priority/standard tier selection is ignored |
| `betas`, `container` | Beta opt-ins and code-execution container reuse are dropped |

If a workload depends on these features, route it to the direct Anthropic
provider instead.
