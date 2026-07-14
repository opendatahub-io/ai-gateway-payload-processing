# Azure OpenAI Provider (`azure-openai`)

## Overview

The `azure-openai` provider routes requests to Azure OpenAI Service. Azure uses the same
request format as OpenAI but with a different path (`/openai/v1/chat/completions`), a different
auth header (`api-key` instead of `Authorization: Bearer`), and includes Azure-specific fields
in responses that the translator strips.

## Configuration

| Field | Value |
|-------|-------|
| Provider type | `azure-openai` |
| Endpoint | `<deployment>.openai.azure.com` (e.g., `testing-azure1.openai.azure.com`) |
| Auth header | `api-key: <API_KEY>` (NOT Authorization: Bearer) |
| API path | `/openai/v1/chat/completions` |
| Request format | OpenAI Chat Completions (pass-through) |
| Response format | Azure-specific fields stripped (`content_filter_results`, `prompt_filter_results`) |

## Response Field Stripping

Azure adds provider-specific fields to responses that are not part of the OpenAI spec:

- `prompt_filter_results` — content safety results for the prompt (top-level)
- `content_filter_results` — content safety results per choice (inside each `choices[]` entry)

The translator removes these fields, returning a clean OpenAI-compatible response.

## ExternalModel Example

```yaml
apiVersion: maas.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: my-azure-model
  namespace: llm
spec:
  provider: azure-openai
  targetModel: gpt-4.1-mini
  endpoint: testing-azure1.openai.azure.com
  credentialRef:
    name: azure-api-key
```

## Secret Example

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: azure-api-key
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: "<AZURE_API_KEY>"
```

## How to Get an API Key

1. Go to Azure Portal → Azure OpenAI → your resource
2. Navigate to "Keys and Endpoint"
3. Copy Key 1 or Key 2
4. The endpoint is shown on the same page (e.g., `https://testing-azure1.openai.azure.com/`)

Note: You also need to create a model deployment in Azure OpenAI Studio before you can use it.

## Supported Models

Depends on your Azure OpenAI deployment. Common models:
- `gpt-4o`, `gpt-4o-mini`
- `gpt-4.1`, `gpt-4.1-mini`
- `gpt-3.5-turbo`

The model name in `targetModel` must match the deployment name in your Azure OpenAI resource.

## Known Limitations

- Azure OpenAI has aggressive rate limiting on testing/free tiers — space out requests
- The `api-key` auth header is Azure-specific (other providers use `Authorization: Bearer`)

## Testing

```bash
# Direct API test (Azure format)
curl -sk "https://testing-azure1.openai.azure.com/openai/v1/chat/completions" \
  -H "api-key: <YOUR_API_KEY>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'

# Through MaaS gateway (OpenAI format)
curl -sk "https://${GATEWAY_HOST}/llm/my-azure-model/v1/chat/completions" \
  -H "Authorization: Bearer ${MAAS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4.1-mini","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'
```

## Official Documentation

- Azure OpenAI API Reference: https://learn.microsoft.com/en-us/azure/ai-services/openai/reference
- Chat Completions: https://learn.microsoft.com/en-us/azure/ai-foundry/openai/latest
- Models: https://learn.microsoft.com/en-us/azure/ai-services/openai/concepts/models
- Quickstart: https://learn.microsoft.com/en-us/azure/ai-services/openai/chatgpt-quickstart
