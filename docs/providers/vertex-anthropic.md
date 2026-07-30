# Vertex AI — Anthropic Claude (`vertex-anthropic`)

## Overview

Routes requests to Vertex AI's Anthropic Claude endpoint. The gateway translates
OpenAI Chat Completions to Anthropic Messages API format and applies Vertex-specific
adjustments (path construction via config, `anthropic_version` in request body,
stripping of unsupported fields).

Native Anthropic SDK clients (e.g., Claude Code) can also target this provider using
`apiFormat: vertex-messages` — body conversion is skipped and only Vertex-specific
adjustments are applied.

## Configuration

| Field | Value |
|-------|-------|
| Provider type | freeform (e.g. `vertex-anthropic`) — used for logging only, not validated |
| Endpoint | `us-central1-aiplatform.googleapis.com` (or your region) |
| Auth | OAuth2 — GCP service account JSON → Bearer token |
| `apiFormat` | `vertex-messages` |
| Path template | `/v1/projects/{project}/locations/{location}/publishers/anthropic/models/{model}:rawPredict` |

## Required config keys

| Key | Description |
|-----|-------------|
| `project` | GCP project ID |
| `location` | GCP region (e.g. `us-central1`) |
| `anthropicVersion` | Anthropic API version string (e.g. `vertex-2023-10-16`) |

The `{model}` placeholder in the path is reserved and auto-resolves to `targetModel`.

## What gets stripped

Vertex AI's Claude endpoint rejects several fields that Anthropic's direct API accepts.
The gateway automatically strips:

**Headers:** `anthropic-beta`

**Body fields:** `context_management`, `betas`, `mcp_servers`, `service_tier`, `container`, `stream_options`

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
      "client_email": "ipp-sa@my-gcp-project.iam.gserviceaccount.com",
      "private_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n"
    }
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: vertex-claude
  namespace: llm
spec:
  provider: vertex-anthropic
  endpoint: us-central1-aiplatform.googleapis.com
  auth:
    type: oauth2
    secretRef:
      name: gcp-sa
  config:
    project: my-gcp-project
    location: us-central1
    anthropicVersion: "vertex-2023-10-16"
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: claude-vertex
  namespace: llm
spec:
  modelName: claude-vertex
  externalProviderRefs:
    - ref:
        name: vertex-claude
      targetModel: claude-sonnet-4-20250514
      apiFormat: vertex-messages
      path: /v1/projects/{project}/locations/{location}/publishers/anthropic/models/{model}:rawPredict
```

## Native Anthropic SDK clients (Claude Code)

Claude Code and other native Anthropic SDK clients send requests in Anthropic Messages format.
Use `apiFormat: vertex-messages` — the body is NOT converted (it's already Anthropic format),
only the Vertex-specific adjustments are applied.

## IAM requirements

```bash
gcloud projects add-iam-policy-binding my-gcp-project \
  --member="serviceAccount:ipp-sa@my-gcp-project.iam.gserviceaccount.com" \
  --role="roles/aiplatform.user"
```

## Testing

```bash
# Through MaaS gateway (OpenAI format — translated automatically)
curl -sk "https://${GATEWAY_HOST}/v1/chat/completions" \
  -H "Authorization: Bearer ${MAAS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-vertex","messages":[{"role":"user","content":"Say hello"}],"max_tokens":10}'
```

## Official Documentation

- Vertex AI partner models: https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/use-claude
- Claude models on Vertex: https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/use-claude#claude-supported-regions
