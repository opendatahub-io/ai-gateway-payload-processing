# Vertex AI OpenAI-Compatible Provider (`vertex-openai`)

## Overview

The `vertex-openai` provider routes requests to Google Vertex AI's OpenAI-compatible
chat/completions endpoint. This is a pass-through translator — the request body is not
mutated since the endpoint accepts OpenAI format natively. The translator rewrites the
`:path` header to include the GCP project, location, and endpoint.

## Important: Plugin Configuration Required

Unlike other providers, `vertex-openai` requires plugin-level configuration with your
GCP project, location, and endpoint. These values are set in the Helm chart's plugin config:

```yaml
plugins:
  - type: api-translation
    name: api-translation
    json:
      vertexOpenAI:
        project: "<GCP_PROJECT_ID>"
        location: "us-central1"
        endpoint: "openapi"
```

Without this config, the `vertex-openai` translator will not be registered and requests
will fail with `unsupported provider - 'vertex-openai'`.

## Authentication

Vertex AI uses OAuth2 authentication. The gateway automatically generates OAuth2 tokens
from a GCP Service Account JSON stored in a Kubernetes Secret.

### How It Works

1. You create a Service Account in GCP and download its JSON key file
2. You store the JSON key in a Kubernetes Secret
3. The gateway reads the JSON, generates an OAuth2 token, and caches it
4. Tokens are automatically refreshed before they expire (~1 hour lifetime)

This is fully automatic — no manual token refresh is needed.

### Secret Format

The Secret must contain a field named `gcp-service-account-json` with the full
Service Account JSON content:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: vertex-credentials
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  gcp-service-account-json: |
    {
      "type": "service_account",
      "project_id": "my-gcp-project",
      "private_key_id": "abc123...",
      "private_key": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n",
      "client_email": "my-service@my-gcp-project.iam.gserviceaccount.com",
      "client_id": "123456789",
      "auth_uri": "https://accounts.google.com/o/oauth2/auth",
      "token_uri": "https://oauth2.googleapis.com/token",
      "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
      "client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/..."
    }
```

### How to Create a Service Account

1. Go to [GCP Console > IAM > Service Accounts](https://console.cloud.google.com/iam-admin/serviceaccounts)
2. Click **Create Service Account**
3. Give it a name (e.g., `vertex-ai-gateway`)
4. Grant the **Vertex AI User** role (`roles/aiplatform.user`)
5. Click **Done**, then click the service account name
6. Go to **Keys** tab → **Add Key** → **Create new key** → **JSON**
7. Download the JSON file
8. Create the Kubernetes Secret:

```bash
kubectl create secret generic vertex-credentials -n llm \
  --from-file=gcp-service-account-json=./my-service-account.json

# Add the required label
kubectl label secret vertex-credentials -n llm \
  inference.llm-d.ai/ipp-managed=true
```

## Configuration

| Field | Value |
|-------|-------|
| Provider type | `vertex-openai` |
| ExternalModel endpoint | `{region}-aiplatform.googleapis.com` (e.g., `us-central1-aiplatform.googleapis.com`) |
| Auth header | `Authorization: Bearer <OAUTH_TOKEN>` (auto-generated) |
| API path | `/v1/projects/{project}/locations/{location}/endpoints/{endpoint}/chat/completions` |
| Request format | OpenAI Chat Completions (pass-through) |
| Response format | `usage.extra_properties` stripped; rest is OpenAI-compatible |
| Secret field | `gcp-service-account-json` (Service Account JSON) |

## Response Field Stripping

Vertex adds `usage.extra_properties` (containing Google-specific metadata like `traffic_type`)
to responses. The translator strips this field automatically.

## ExternalModel Example

```yaml
apiVersion: maas.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: my-vertex-model
  namespace: llm
spec:
  provider: vertex-openai
  targetModel: google/gemini-2.5-flash
  endpoint: us-central1-aiplatform.googleapis.com
  credentialRef:
    name: vertex-credentials
```

Note: The `targetModel` must use `publisher/model` format (e.g., `google/gemini-2.5-flash`).

## Supported Models

Any model available on Vertex AI's OpenAI-compatible endpoint:
- `google/gemini-2.5-flash`, `google/gemini-2.5-pro`
- `google/gemini-2.0-flash`
- Third-party models deployed on Vertex AI

Use the `publisher/model` format for all model names.

## Troubleshooting

**`credentials missing required field gcp-service-account-json`:**
The Secret is missing the `gcp-service-account-json` field. Ensure the Secret contains
the full Service Account JSON under that field name.

**`failed to parse service account JSON`:**
The content in `gcp-service-account-json` is not valid JSON. Check that you copied
the entire Service Account JSON file content.

**`failed to obtain OAuth2 token`:**
The Service Account credentials are invalid or the account doesn't have permission
to access Vertex AI. Verify:
- The JSON is a valid Service Account key (not a user account)
- The Service Account has the `Vertex AI User` role
- The `private_key` field contains a valid RSA private key

**404 on inference:**
- Check that `targetModel` uses `publisher/model` format (e.g., `google/gemini-2.5-flash`,
  not just `gemini-2.5-flash`)
- Verify the API version — `v1beta` may return 404; the translator uses `v1`

## Testing

```bash
# Direct API test (Vertex native OpenAI-compatible)
curl -sk "https://us-central1-aiplatform.googleapis.com/v1/projects/<PROJECT>/locations/us-central1/endpoints/openapi/chat/completions" \
  -H "Authorization: Bearer $(gcloud auth print-access-token)" \
  -H "Content-Type: application/json" \
  -d '{"model":"google/gemini-2.5-flash","messages":[{"role":"user","content":"Say hello"}],"max_tokens":100}'

# Through MaaS gateway
curl -sk "https://${GATEWAY_HOST}/llm/my-vertex-model/v1/chat/completions" \
  -H "Authorization: Bearer ${MAAS_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"google/gemini-2.5-flash","messages":[{"role":"user","content":"Say hello"}],"max_tokens":100}'
```

## Official Documentation

- Vertex AI OpenAI-compatible API: https://cloud.google.com/vertex-ai/generative-ai/docs/multimodal/call-gemini-using-openai-library
- Chat Completions: https://cloud.google.com/vertex-ai/generative-ai/docs/reference/rest/v1/projects.locations.endpoints.chat/completions
- Models: https://cloud.google.com/vertex-ai/generative-ai/docs/learn/models
- Service Account Authentication: https://cloud.google.com/docs/authentication/provide-credentials-adc
