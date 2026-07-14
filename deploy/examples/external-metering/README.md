# External Metering — Example Deployment

Dev Preview: per-user token usage tracking and cost attribution for the AI Inference Gateway.

**RHAISTRAT-1919** | Not included in the default plugin chain — operators enable manually.

## Architecture

```
Client → Gateway → IPP Plugin Chain
                      ├── model-provider-resolver
                      ├── external-metering ← checks balance, reports usage
                      ├── api-translation
                      └── apikey-injection
                              ↓
                    OpenMeter (or compatible metering backend)
                      ├── POST /api/v1/events (CloudEvents ingestion)
                      └── GET /api/v1/customers/{id}/entitlements/{key}/value
```

## Prerequisites

- RHOAI cluster with AI Inference Gateway deployed
- `oc` CLI logged in with cluster-admin
- A metering backend that accepts CloudEvents v1.0 (e.g., [OpenMeter](https://openmeter.io))

## Deploy OpenMeter

OpenMeter can run as a pod within the cluster. See the [OpenMeter quickstart](https://openmeter.io/docs/getting-started/quickstart) for deployment options.

Example using their Helm chart:

```bash
helm repo add openmeter https://openmeter.github.io/helm-charts
helm install openmeter openmeter/openmeter -n metering --create-namespace
```

## Enable the Plugin

Merge `values-with-metering.yaml` with your existing Helm values, updating `meteringURL` to point to your OpenMeter (or compatible) service:

```bash
helm upgrade ai-gateway <chart> -f values-with-metering.yaml
```

## Plugin Configuration

```yaml
- type: external-metering
  name: external-metering
  json:
    meteringURL: "https://openmeter.metering.svc:8443"
    timeoutSeconds: 5
    featureKey: "inference-tokens"
    source: "maas-gateway"
    failOpen: true
```

| Field | Default | Description |
|-------|---------|-------------|
| `meteringURL` | (required) | Metering backend endpoint (OpenMeter or compatible) |
| `timeoutSeconds` | `5` | HTTP timeout for balance checks |
| `featureKey` | `inference-tokens` | Entitlement key for quota lookup |
| `source` | `maas-gateway` | CloudEvents source identifier |
| `failOpen` | `true` | Allow requests when metering backend is unavailable |

## CloudEvents Format

The plugin emits events in [CloudEvents v1.0](https://cloudevents.io) format:

```json
{
  "specversion": "1.0",
  "id": "evt-<uuid>",
  "source": "maas-gateway",
  "type": "inference.tokens.used",
  "subject": "<username>",
  "time": "2026-06-10T12:00:00Z",
  "datacontenttype": "application/json",
  "data": {
    "user": "<username>",
    "group": "<group>",
    "subscription": "<subscription>",
    "provider": "anthropic",
    "model": "claude-opus-4-8",
    "prompt_tokens": 150,
    "completion_tokens": 80,
    "total_tokens": 230,
    "cached_input_tokens": 12000,   // 0 when provider does not report
    "cache_creation_tokens": 100,   // 0 when provider does not report
    "reasoning_tokens": 0,          // 0 when provider does not report
    "duration_ms": 1200
  }
}
```

## Token Types Tracked

| Token Type | OpenAI field | Anthropic field |
|-----------|-------------|-----------------|
| Input (new) | `prompt_tokens` | `input_tokens` |
| Output | `completion_tokens` | `output_tokens` |
| Cached read | `prompt_tokens_details.cached_tokens` | `cache_read_input_tokens` |
| Cache write | — | `cache_creation_input_tokens` |
| Reasoning | `completion_tokens_details.reasoning_tokens` | — |

## Testing with the Development Metering Service

For testing without OpenMeter, a standalone development metering service with PostgreSQL and built-in dashboards is available at:
[noyitz/ai-gateway-metering-service](https://github.com/noyitz/ai-gateway-metering-service)
