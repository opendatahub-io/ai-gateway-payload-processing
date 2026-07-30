# Vertex AI — OpenAI-compatible (`vertex-openai`)

## Overview

Routes requests to Vertex AI's OpenAI-compatible endpoint, which serves Gemini models
in OpenAI Chat Completions format. No body conversion is needed — the gateway only
rewrites the `:path` and handles GCP OAuth2 authentication.

## Configuration

| Field | Value |
|-------|-------|
| Provider type | `vertex-openai` |
| Endpoint | `us-central1-aiplatform.googleapis.com` (or your region) |
| Auth | OAuth2 — GCP service account JSON → Bearer token |
| `apiFormat` | `openai-chat` |
| Path template | `/v1/projects/{project}/locations/{location}/endpoints/{endpoint}/chat/completions` |

## Required config keys

| Key | Description |
|-----|-------------|
| `project` | GCP project ID |
| `location` | GCP region (e.g. `us-central1`) |
| `endpoint` | Vertex AI endpoint name (typically `openapi`) |

## Example

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
    {
      "type": "service_account",
      "project_id": "my-gcp-project",
      "private_key_id": "...",
      "private_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n",
      "client_email": "ipp-sa@my-gcp-project.iam.gserviceaccount.com"
    }
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: vertex-gemini
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
  name: gemini-flash
  namespace: llm
spec:
  externalProviderRefs:
    - ref:
        name: vertex-gemini
      targetModel: gemini-2.0-flash
      apiFormat: openai-chat
      path: /v1/projects/{project}/locations/{location}/endpoints/{endpoint}/chat/completions
```

## Stripped response fields

The `usage.extra_properties` field returned by Vertex is stripped from responses.

## IAM requirements

The service account must have the `roles/aiplatform.user` role on the GCP project.

```bash
gcloud projects add-iam-policy-binding my-gcp-project \
  --member="serviceAccount:ipp-sa@my-gcp-project.iam.gserviceaccount.com" \
  --role="roles/aiplatform.user"
```

## Testing

```bash
# Through MaaS gateway
curl -sk "https://${GATEWAY_HOST}/v1/chat/completions" \
  -H "Authorization: Bearer ${MAAS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-flash","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'
```

## Official Documentation

- Vertex AI OpenAI compatibility: https://cloud.google.com/vertex-ai/generative-ai/docs/multimodal/call-gemini-using-openai-library
- Models: https://cloud.google.com/vertex-ai/generative-ai/docs/learn/models
