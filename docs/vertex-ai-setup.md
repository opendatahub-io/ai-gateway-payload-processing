# Setting Up Vertex AI as an ExternalProvider

This guide covers configuring Vertex AI (Claude models via Google Cloud)
as an ExternalProvider on MaaS.

## Prerequisites

- A GCP service account JSON key file with access to Vertex AI Anthropic models
- Claude models enabled in the GCP project's Model Garden
- MaaS deployed with IPP (payload-processing) that includes the `apikey-injection` plugin with OAuth2 support

## 1. Create the Secret

The secret must contain the GCP service account JSON key and have the
`ipp-managed` label so the `apikey-injection` plugin can find it:

```bash
oc create secret generic vertex-sa-key \
  --from-file=gcp-service-account-json=<your-service-account-key>.json \
  -n <model-namespace>

oc label secret vertex-sa-key \
  inference.llm-d.ai/ipp-managed=true \
  -n <model-namespace>
```

## 2. Create the ExternalProvider

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: vertex
  namespace: <model-namespace>
spec:
  provider: vertex
  endpoint: aiplatform.googleapis.com
  auth:
    type: oauth2
    secretRef:
      name: vertex-sa-key
  config:
    project: <gcp-project-id>
    location: global
    anthropicVersion: vertex-2023-10-16
```

### Provider config fields

| Field | Value | Notes |
|-------|-------|-------|
| `provider` | `vertex` | Triggers Vertex-specific request translation |
| `endpoint` | `aiplatform.googleapis.com` | Global Vertex AI endpoint |
| `auth.type` | `oauth2` | IPP generates OAuth2 tokens from the service account key automatically |
| `config.project` | Your GCP project ID | Must match the project where Claude models are enabled |
| `config.location` | `global` | Required for Opus 4.8; other models may support regional endpoints |
| `config.anthropicVersion` | `vertex-2023-10-16` | Required Anthropic API version header for Vertex |

## 3. Add Vertex to an ExternalModel

Add a Vertex provider ref to an existing ExternalModel, or create a new one:

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: <model-name>
  namespace: <model-namespace>
spec:
  externalProviderRefs:
  - ref:
      name: vertex
    targetModel: <model-name>
    apiFormat: vertex-messages
    path: /v1/projects/{project}/locations/{location}/publishers/anthropic/models/<model-name>:rawPredict
    weight: 100
```

### Model config fields

| Field | Value | Notes |
|-------|-------|-------|
| `apiFormat` | `vertex-messages` | Triggers Vertex Anthropic request/response translation |
| `path` | See above | `{project}` and `{location}` are resolved from the provider config at runtime |
| `targetModel` | e.g. `claude-opus-4-8` | Model name **without** date suffix |
| `weight` | `100` | Set to 0 to disable this provider |

## 4. Test

```bash
export GATEWAY="https://<your-gateway-host>"
export API_KEY="<your-maas-api-key>"

curl -sk \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  "$GATEWAY/v1/chat/completions" \
  -d '{
    "model": "<model-name>",
    "messages": [{"role": "user", "content": "say hello"}],
    "max_tokens": 50
  }'
```

## Important Notes

- **No date suffix**: Use `claude-opus-4-8`, not `claude-opus-4-8-20250527`. Vertex rejects date-suffixed model names.
- **Location must be `global`**: Regional endpoints may not have all models available.
- **OAuth2 is automatic**: The IPP `apikey-injection` plugin reads the service account JSON from the secret and generates short-lived OAuth2 tokens. No manual token management needed.
- **`stream_options` stripped automatically**: The IPP `api-translation` plugin strips `stream_options` from requests to Vertex (Vertex rejects this field).
- **Multi-provider routing**: You can add Vertex alongside other providers (Anthropic direct, OpenAI, simulator) on the same ExternalModel using weight-based routing. Set `weight: 0` to disable a provider without removing it.

## Sharing Service Account Keys

Use [Bitwarden Send](https://vault.bitwarden.com) to share JSON key files securely:
- Set expiration to 48 hours
- Set max access count to 1
- Delete after accessed

Never paste service account keys in Slack or other messaging platforms.
