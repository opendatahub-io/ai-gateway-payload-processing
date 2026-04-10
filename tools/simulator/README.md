# LLM Provider Simulator

Mock simulator for external model providers, used for BBR plugin development, plugin chain testing, and e2e testing.

Related issue: [#18](https://github.com/opendatahub-io/ai-gateway-payload-processing/issues/18)

## Quick Start

```bash
pip install llm-katan

# Echo mode (instant startup, no model download, no GPU)
llm-katan --model my-test-model --backend echo --providers openai,anthropic,vertexai,bedrock,azure_openai

# Real model (downloads ~1GB, needs torch)
llm-katan --model Qwen/Qwen3-0.6B --providers openai,anthropic,vertexai,bedrock,azure_openai
```

## Supported Providers

| Provider | Endpoint | Auth Header |
|----------|----------|-------------|
| OpenAI | `POST /v1/chat/completions` | `Authorization: Bearer <key>` |
| Anthropic | `POST /v1/messages` | `x-api-key: <key>` |
| Vertex AI / Gemini | `POST /v1beta/models/{model}:generateContent` | `Authorization: Bearer <token>` |
| AWS Bedrock | `POST /model/{modelId}/converse` | `Authorization: AWS4-HMAC-SHA256 <sig>` |
| AWS Bedrock | `POST /model/{modelId}/invoke` | `Authorization: AWS4-HMAC-SHA256 <sig>` |
| Azure OpenAI | `POST /openai/deployments/{id}/chat/completions` | `api-key: <key>` |

All endpoints require auth headers. All endpoints support streaming.

### API Key Validation

By default, the simulator only checks that auth headers are present (any value accepted). To validate actual key values, use `--validate-keys`:

```bash
llm-katan --model test --backend echo --validate-keys --providers openai,anthropic,vertexai,bedrock,azure_openai
```

Default keys per provider (used when `--validate-keys` is enabled):

| Provider | Default Key |
|----------|------------|
| OpenAI | `llm-katan-openai-key` |
| Anthropic | `llm-katan-anthropic-key` |
| Vertex AI | `llm-katan-vertexai-key` |
| Bedrock | `llm-katan-bedrock-key` |
| Azure OpenAI | `llm-katan-azure-key` |

Override specific keys: `--api-keys openai=custom-key,anthropic=other-key`

When a wrong key is sent, the error response includes the expected key — because this is a test simulator, not a security boundary.

### Bedrock InvokeModel Families

The `/model/{modelId}/invoke` endpoint auto-detects the model family from the model ID:

| Family | Model ID Prefix | Request Format |
|--------|----------------|----------------|
| Anthropic Claude | `anthropic.*` | `messages[]`, `max_tokens`, `system` |
| Amazon Nova | `amazon.nova*` | `messages[].content[].text`, `inferenceConfig` |
| Amazon Titan | `amazon.titan*` | `inputText`, `textGenerationConfig` |
| Meta Llama | `meta.llama*` | `prompt`, `max_gen_len` |
| Cohere Command | `cohere.*` | `message`, `chat_history[]` |
| Mistral | `mistral.*` | `prompt`, `max_tokens` |
| DeepSeek | `deepseek.*` | `prompt`, `max_tokens` |
| AI21 Jamba | `ai21.*` | `messages[]` (OpenAI-like) |

## Example Requests

### OpenAI
```bash
curl -X POST http://localhost:8000/v1/chat/completions \
  -H "Authorization: Bearer test-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}]}'
```

### Anthropic
```bash
curl -X POST http://localhost:8000/v1/messages \
  -H "x-api-key: test-key" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-sonnet","max_tokens":100,"messages":[{"role":"user","content":"Hello"}]}'
```

### Vertex AI / Gemini
```bash
curl -X POST http://localhost:8000/v1beta/models/gemini-pro:generateContent \
  -H "Authorization: Bearer test-token" \
  -H "Content-Type: application/json" \
  -d '{"contents":[{"role":"user","parts":[{"text":"Hello"}]}]}'
```

### AWS Bedrock (Converse)
```bash
curl -X POST http://localhost:8000/model/anthropic.claude-v2/converse \
  -H "Authorization: AWS4-HMAC-SHA256 Credential=test" \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":[{"text":"Hello"}]}]}'
```

### AWS Bedrock (InvokeModel — Claude)
```bash
curl -X POST http://localhost:8000/model/anthropic.claude-v2/invoke \
  -H "Authorization: AWS4-HMAC-SHA256 Credential=test" \
  -H "Content-Type: application/json" \
  -d '{"anthropic_version":"bedrock-2023-05-31","max_tokens":100,"messages":[{"role":"user","content":"Hello"}]}'
```

### Azure OpenAI
```bash
curl -X POST http://localhost:8000/openai/deployments/gpt-4/chat/completions?api-version=2024-10-21 \
  -H "api-key: test-key" \
  -H "Content-Type: application/json" \
  -d '{"messages":[{"role":"user","content":"Hello"}]}'
```

## Shared Endpoints

```bash
curl http://localhost:8000/          # Server info
curl http://localhost:8000/health    # Health check
curl http://localhost:8000/metrics   # Prometheus metrics
```

## How It Works

The simulator does not proxy to real providers. Each provider is a formatting layer around the same backend:

```
Request (any provider format)
       |
Provider (openai / anthropic / vertexai / bedrock / azure_openai)
  - Parses provider-specific request
  - Extracts: messages, max_tokens, temperature
       |
Backend (echo or real model)
  - Generates text (or echoes request metadata)
       |
Provider (same one)
  - Formats response in provider's native format
  - Returns to client
```

No translation chain, no SDK calls, no cloud API costs.

## CLI Options

```
llm-katan [OPTIONS]

Required:
  -m, --model TEXT              Model name (or any string in echo mode)

Optional:
  -b, --backend [transformers|vllm|echo]  Backend (default: transformers)
  --providers TEXT              Comma-separated providers (default: openai)
  -p, --port INTEGER            Port (default: 8000)
  -n, --served-model-name TEXT  Model name in API responses
  --max-tokens INTEGER          Max tokens (default: 512)
  -t, --temperature FLOAT       Temperature (default: 0.7)
  --max-concurrent INTEGER      Concurrent requests (default: 1)
  --quantize/--no-quantize      CPU int8 quantization (default: enabled)
  --tls                         Enable HTTPS with self-signed certificate
  --validate-keys               Validate API key values (uses defaults or --api-keys overrides)
  --api-keys TEXT               Override keys: openai=mykey,anthropic=mykey2
```
