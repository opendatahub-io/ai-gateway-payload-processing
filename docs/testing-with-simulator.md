# Testing ExternalModel with the Simulator

This guide provides working CRDs and curl commands for testing ExternalModel
body-based routing against the shared simulator (`llm-katan`). The simulator
supports OpenAI Chat Completions and Anthropic Messages formats.

## Simulator Endpoint

```
Host: 3-132-132-211.sslip.io
Providers: openai, anthropic, azure, bedrock, vertexai
```

Each provider type has its own API key:

| Provider | Key |
|----------|-----|
| openai | `llm-katan-openai-key` |
| anthropic | `llm-katan-anthropic-key` |
| azure | `llm-katan-azure-key` |
| bedrock | `llm-katan-bedrock-key` |

## Secrets

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: sim-openai-key
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: "llm-katan-openai-key"
---
apiVersion: v1
kind: Secret
metadata:
  name: sim-anthropic-key
  namespace: llm
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: "llm-katan-anthropic-key"
```

## ExternalProviders

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: sim-openai
  namespace: llm
spec:
  provider: openai
  endpoint: 3-132-132-211.sslip.io
  auth:
    type: apikey
    secretRef:
      name: sim-openai-key
---
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: sim-anthropic
  namespace: llm
spec:
  provider: anthropic
  endpoint: 3-132-132-211.sslip.io
  auth:
    type: apikey
    secretRef:
      name: sim-anthropic-key
```

## ExternalModels

### 1. OpenAI Chat Completions (`/v1/chat/completions`)

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: sim-chat
  namespace: llm
spec:
  externalProviderRefs:
  - ref:
      name: sim-openai
    targetModel: sim-chat
    apiFormat: openai-chat
    path: /v1/chat/completions
    weight: 100
```

### 2. Anthropic Messages (`/v1/messages`)

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: sim-messages
  namespace: llm
spec:
  externalProviderRefs:
  - ref:
      name: sim-anthropic
    targetModel: sim-messages
    apiFormat: messages
    path: /v1/messages
    weight: 100
```

### 3. OpenAI Chat Completions (alternate — second model for multi-model testing)

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: sim-chat-2
  namespace: llm
spec:
  externalProviderRefs:
  - ref:
      name: sim-openai
    targetModel: sim-chat-2
    apiFormat: openai-chat
    path: /v1/chat/completions
    weight: 100
```

## MaaSModelRefs (required for MaaS governance)

```yaml
apiVersion: maas.opendatahub.io/v1alpha1
kind: MaaSModelRef
metadata:
  name: sim-chat
  namespace: llm
spec:
  modelRef:
    kind: ExternalModel
    name: sim-chat
---
apiVersion: maas.opendatahub.io/v1alpha1
kind: MaaSModelRef
metadata:
  name: sim-messages
  namespace: llm
spec:
  modelRef:
    kind: ExternalModel
    name: sim-messages
---
apiVersion: maas.opendatahub.io/v1alpha1
kind: MaaSModelRef
metadata:
  name: sim-chat-2
  namespace: llm
spec:
  modelRef:
    kind: ExternalModel
    name: sim-chat-2
```

## Curl Commands

Set these variables first:

```bash
export GATEWAY="https://<your-gateway-host>"
export API_KEY="<your-maas-api-key>"
```

### Chat Completions (path-based routing)

```bash
curl -sk \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  "$GATEWAY/llm/sim-chat/v1/chat/completions" \
  -d '{
    "model": "sim-chat",
    "messages": [{"role": "user", "content": "hello"}],
    "max_tokens": 50
  }'
```

### Chat Completions (body-based routing — BBR)

```bash
curl -sk \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  "$GATEWAY/v1/chat/completions" \
  -d '{
    "model": "sim-chat",
    "messages": [{"role": "user", "content": "hello"}],
    "max_tokens": 50
  }'
```

### Messages (body-based routing — BBR)

```bash
curl -sk \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  "$GATEWAY/v1/messages" \
  -d '{
    "model": "sim-messages",
    "messages": [{"role": "user", "content": "hello"}],
    "max_tokens": 50
  }'
```

### Streaming Chat Completions

```bash
curl -sk -N \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  "$GATEWAY/v1/chat/completions" \
  -d '{
    "model": "sim-chat",
    "messages": [{"role": "user", "content": "hello"}],
    "max_tokens": 50,
    "stream": true
  }'
```

### Wrong model (should be rejected — validates BBR is active)

```bash
curl -sk \
  -H "Content-Type: application/json" \
  -H "x-api-key: $API_KEY" \
  "$GATEWAY/v1/chat/completions" \
  -d '{
    "model": "nonexistent-model",
    "messages": [{"role": "user", "content": "hello"}],
    "max_tokens": 50
  }'
```

## Simulator Endpoint

- Host: `3-132-132-211.sslip.io`
- Providers: openai, anthropic, azure, bedrock, vertexai
- Models namespace: `llm` (default)
