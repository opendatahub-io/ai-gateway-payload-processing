# Provider Guides

Configuration guides for each supported external model provider.

| Provider | `apiFormat` | Translation | Auth | Guide |
|----------|-------------|------------|------|-------|
| [OpenAI](openai.md) | `openai-chat` | Pass-through | `Authorization: Bearer` | [openai.md](openai.md) |
| [Anthropic](anthropic.md) | `messages` | OpenAI ↔ Messages API | `x-api-key` | [anthropic.md](anthropic.md) |
| [AWS Bedrock](bedrock-openai.md) | `openai-chat` | Pass-through | `Authorization: Bearer` (Mantle API key) | [bedrock-openai.md](bedrock-openai.md) |
| [Azure OpenAI](azure-openai.md) | `openai-chat` | Path rewrite | `api-key` | [azure-openai.md](azure-openai.md) |
| [Vertex AI (Gemini)](vertex-openai.md) | `openai-chat` | Path rewrite + OAuth2 | Bearer (OAuth2) | [vertex-openai.md](vertex-openai.md) |
| [Vertex AI (Claude)](vertex-anthropic.md) | `vertex-messages` | OpenAI → Anthropic + Vertex | Bearer (OAuth2) | [vertex-anthropic.md](vertex-anthropic.md) |

## Architecture guides

- [ExternalProvider and ExternalModel CRDs](../external-provider-model.md) — full reference for the two-resource architecture
- [Multi-provider traffic splitting](../multi-provider.md) — weighted routing, credential rotation, per-binding config

## Credential secrets

Every provider's API-key Secret **must** carry the label:

```yaml
metadata:
  labels:
    inference.llm-d.ai/ipp-managed: "true"
```

The apikey-injection plugin's reconciler only watches Secrets with this label
(`pkg/plugins/apikey-injection/reconciler.go`). A Secret without it is invisible
to the credential store even if the `ExternalProvider`'s `secretRef` points at it,
and every request to that provider fails with:

```
HTTP 500 — inference error: Internal - authType 'apikey' credentials not found
```

To fix an existing unlabeled Secret:

```bash
kubectl label secret <name> -n <namespace> inference.llm-d.ai/ipp-managed=true
```
