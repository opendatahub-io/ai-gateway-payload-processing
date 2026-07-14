# AWS Bedrock OpenAI-Compatible Provider (`bedrock-openai`)

## Overview

The `bedrock-openai` provider routes requests to AWS Bedrock's OpenAI-compatible endpoint.
This is a pass-through translator — the request body is not mutated since Bedrock's
OpenAI-compatible API accepts the same format as OpenAI.

## Important: Bedrock Runtime vs Bedrock Mantle

AWS has **two different Bedrock endpoints** with different path formats:

| Endpoint | Path | Auth | Status |
|----------|------|------|--------|
| `bedrock-runtime.{region}.amazonaws.com` | `/openai/v1/chat/completions` | AWS SigV4 / IAM | **NOT supported** — path has `/openai/` prefix |
| `bedrock-mantle.{region}.api.aws` | `/v1/chat/completions` | Bearer token (Bedrock API Key) | **Supported** |

**You must use `bedrock-mantle`**, not `bedrock-runtime`. The translator hardcodes the path to
`/v1/chat/completions` which only works with Bedrock Mantle.

If you use `bedrock-runtime`, you will get a `404 route_not_found` error because the path doesn't
match (`/openai/v1/chat/completions` vs `/v1/chat/completions`).

### What is Bedrock Mantle?

Bedrock Mantle (Project Mantle) is AWS's OpenAI-compatible inference gateway, announced at
AWS re:Invent in late 2025. Key differences from Bedrock Runtime:

- **OpenAI-compatible API** — uses standard OpenAI Chat Completions format
- **Simple auth** — Bearer tokens and Bedrock API Keys instead of SigV4
- **OpenAI SDK compatible** — change `OPENAI_BASE_URL` and it works
- **Growing model catalog** — supports a subset of Bedrock models (actively expanding)

## Configuration

| Field | Value |
|-------|-------|
| Provider type | `bedrock-openai` |
| Endpoint | `bedrock-mantle.{region}.api.aws` (e.g., `bedrock-mantle.us-east-2.api.aws`) |
| Auth header | `Authorization: Bearer <BEDROCK_API_KEY>` |
| API path | `/v1/chat/completions` |
| Request format | OpenAI Chat Completions (pass-through) |
| Response format | OpenAI Chat Completions (pass-through) |

## ExternalModel Example

```yaml
apiVersion: maas.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: my-bedrock-model
  namespace: llm
spec:
  provider: bedrock-openai
  targetModel: openai.gpt-oss-20b
  endpoint: bedrock-mantle.us-east-2.api.aws
  credentialRef:
    name: bedrock-api-key
```

## Secret Example

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: bedrock-api-key
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: "<BEDROCK_API_KEY>"
```

## How to Get a Bedrock API Key

1. Go to the AWS Console → Amazon Bedrock → API Keys
2. Create a new Bedrock API Key
3. Copy the key — it starts with `ABSK` and is a base64-encoded string

When decoded, a Bedrock API Key looks like: `BedrockAPIKey-{id}:{secret}`.
This is NOT the same as AWS Access Keys (used for SigV4 auth with `bedrock-runtime`).
Bedrock API Keys are specifically for the Mantle OpenAI-compatible endpoint and use
simple Bearer token authentication.

## Supported Models

List available models on your endpoint:

```bash
curl -sk "https://bedrock-mantle.us-east-2.api.aws/v1/models" \
  -H "Authorization: Bearer <YOUR_API_KEY>"
```

Common models include:
- `openai.gpt-oss-20b`, `openai.gpt-oss-120b` (OpenAI open-weight)
- `mistral.ministral-3-8b-instruct`, `mistral.mistral-large-3-675b-instruct`
- `deepseek.v3.1`, `deepseek.v3.2`
- `google.gemma-3-27b-it`
- `qwen.qwen3-32b`

Note: Anthropic Claude models are NOT available via Bedrock Mantle. Use the `anthropic`
provider type with `api.anthropic.com` for Claude models.

## Troubleshooting

**404 `route_not_found`:**
You're using `bedrock-runtime.{region}.amazonaws.com` instead of `bedrock-mantle.{region}.api.aws`.
Change your ExternalModel endpoint.

**401 `UnauthorizedException`:**
Your Bedrock API Key is invalid or expired. Generate a new one from the AWS Console.

**`model not found`:**
The model is not available on your Bedrock Mantle endpoint. Use `/v1/models` to list
available models. Model availability varies by region and account.

**Reasoning models (`content: null`):**
Some models (like `openai.gpt-oss-20b`) are reasoning models that return thinking in a
`reasoning` field. Use `max_tokens: 100` or higher to allow the model to finish thinking
and produce output in `content`.

## Testing

```bash
# List available models
curl -sk "https://bedrock-mantle.us-east-2.api.aws/v1/models" \
  -H "Authorization: Bearer <YOUR_API_KEY>"

# Direct API test
curl -sk "https://bedrock-mantle.us-east-2.api.aws/v1/chat/completions" \
  -H "Authorization: Bearer <YOUR_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"model":"mistral.ministral-3-8b-instruct","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'

# Through MaaS gateway
curl -sk "https://${GATEWAY_HOST}/llm/my-bedrock-model/v1/chat/completions" \
  -H "Authorization: Bearer ${MAAS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai.gpt-oss-20b","messages":[{"role":"user","content":"Say hello"}],"max_tokens":100}'
```

## Official Documentation

- Bedrock OpenAI-compatible API: https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html
- Bedrock Models: https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-openai.html
- Bedrock API Keys: https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html
