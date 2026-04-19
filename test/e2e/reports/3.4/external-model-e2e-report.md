# ExternalModel E2E Test Plan & Results

**Date:** 2026-04-19 (updated)  
**Version:** MaaS 3.4 (maas-controller + maas-api + BBR payload-processing)  
**Platform:** RHOAI on OCP (AWS)

---

## Overview

This document serves as both a **test plan** and **test report** for the ExternalModel feature.
Each test section describes what is being tested, provides the curl command to reproduce,
and shows side-by-side results for real providers vs. the llm-katan simulator.

The goal is to validate that:
1. Real external providers (OpenAI, Anthropic, Bedrock, Gemini) work end-to-end through the MaaS + BBR pipeline
2. The llm-katan simulator produces consistent behavior (same HTTP codes, same response format)
3. All error paths return correct status codes

## Setup

### Prerequisites

```bash
# 1. Login to OpenShift
oc login <CLUSTER_API> -u <USER> -p <PASS> --insecure-skip-tls-verify

# 2. Set variables
export GATEWAY_HOST="<GATEWAY_HOSTNAME>"
export TOKEN=$(oc whoami -t)
```

### Mint a MaaS API Key

```bash
curl -sk "https://${GATEWAY_HOST}/maas-api/v1/api-keys" \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"name":"e2e-test-key","subscription":"external-models-subscription"}'

# Copy the "key" field:
export API_KEY="<RETURNED_KEY>"
```

### Models Under Test

| Model Name | Provider | Backend | targetModel | Description |
|------------|----------|---------|-------------|-------------|
| `ext-openai` | openai | Real (api.openai.com) | gpt-4o-mini | Real OpenAI API |
| `ext-anthropic` | anthropic | Real (api.anthropic.com) | claude-haiku-4-5-20251001 | Real Anthropic API, translated from OpenAI format |
| `ext-bedrock` | bedrock-openai | Real (Bedrock Mantle) | openai.gpt-oss-20b | Real AWS Bedrock OpenAI-compatible API |
| `ext-gemini` | openai | Real (generativelanguage.googleapis.com) | gemini-2.5-flash | Google Gemini OpenAI-compatible API |
| `sim-openai` | openai | Simulator (llm-katan) | sim-openai | Simulates OpenAI provider |
| `sim-anthropic` | anthropic | Simulator (llm-katan) | sim-anthropic | Simulates Anthropic provider |
| `sim-bedrock` | bedrock-openai | Simulator (llm-katan) | sim-bedrock | Simulates Bedrock provider |
| `facebook-opt-125m-simulated` | (internal) | KServe pod (cluster-local) | N/A | Internal LLMInferenceService model |

### Providers Not Yet Tested

| Provider | Reason | Tracking |
|----------|--------|----------|
| Azure OpenAI (`azure-openai`) | No API key available for testing | Need Azure OpenAI access with deployment |
| Vertex AI (`vertex`) | Requires GCP service account + OAuth | Need GCP project with Vertex AI enabled |
| Gemini (`ext-gemini`) | Path incompatibility (see Known Gaps) | [#143](https://github.com/opendatahub-io/ai-gateway-payload-processing/issues/143) |

---

## Test 1: Basic Chat Completions

**What we test:** The full happy-path request flow — MaaS auth (Kuadrant) -> BBR plugin chain
(model resolution -> API translation -> credential injection) -> provider API -> response
translation back to OpenAI format.

**What we verify:**
- HTTP 200 response
- Response body contains `choices[].message.content`
- Response contains `usage.prompt_tokens` and `usage.completion_tokens`
- `finish_reason` is `stop` or `length`

### Curl Command

```bash
# OpenAI (real)
curl -sk "https://${GATEWAY_HOST}/llm/ext-openai/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Say hello in one word."}],"max_tokens":10}'

# Anthropic (real) — request translated from OpenAI -> Anthropic Messages API
curl -sk "https://${GATEWAY_HOST}/llm/ext-anthropic/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-haiku-4-5-20251001","messages":[{"role":"user","content":"Say hello in one word."}],"max_tokens":10}'

# Bedrock (real) — OpenAI-compatible pass-through
curl -sk "https://${GATEWAY_HOST}/llm/ext-bedrock/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"openai.gpt-oss-20b","messages":[{"role":"user","content":"Say hello in one word."}],"max_tokens":10}'

# Simulator — replace model name and path: sim-openai / sim-anthropic / sim-bedrock
curl -sk "https://${GATEWAY_HOST}/llm/sim-openai/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"sim-openai","messages":[{"role":"user","content":"Say hello in one word."}],"max_tokens":10}'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (200, `stop`, content="Hello!") | PASS (200, `stop`, echo) | Yes |
| anthropic | PASS (200, `stop`, content="Hello!") | PASS (200, `stop`, echo) | Yes |
| bedrock-openai | PASS (200, `length`, reasoning model) | PASS (200, `stop`, echo) | Yes |
| gemini | **FAIL** (404 — path incompatibility) | N/A | N/A |

> **Gemini gap:** The `openai` translator rewrites the path to `/v1/chat/completions`, but Gemini
> only serves at `/v1beta/openai/chat/completions`. See [#143](https://github.com/opendatahub-io/ai-gateway-payload-processing/issues/143).

---

## Test 2: Streaming (SSE)

**What we test:** Server-Sent Events streaming — the client receives chunked `data: {...}` lines
in real time instead of waiting for the full response.

**What we verify:**
- Response content-type is `text/event-stream`
- Response contains `data: {...}` chunks with `choices[].delta.content`
- Final chunk is `data: [DONE]`

### Curl Command

```bash
curl -sk --no-buffer "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<TARGET_MODEL>",
    "messages": [{"role": "user", "content": "Count from 1 to 5."}],
    "max_tokens": 50,
    "stream": true
  }'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (SSE chunks + `[DONE]`) | PASS (SSE chunks) | Yes |
| anthropic | PASS (SSE chunks) | PASS (SSE chunks) | Yes |
| bedrock-openai | PASS (SSE chunks) | PASS (SSE chunks) | Yes |

> **Previously failing (fixed):** The Anthropic translator was dropping the `stream` field.
> Fixed in [PR #137](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/137).

---

## Test 3: System Message Handling

**What we test:** That the `system` role message is correctly handled by each provider.
For Anthropic, the BBR translator extracts system messages into the top-level `system`
field (Anthropic's native format). For OpenAI and Bedrock, system messages pass through as-is.

**What we verify:**
- HTTP 200
- Response content reflects the system message instruction (e.g., pirate speak)

### Curl Command

```bash
curl -sk "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<TARGET_MODEL>",
    "messages": [
      {"role": "system", "content": "You are a pirate. Respond only in pirate speak."},
      {"role": "user", "content": "What is the weather like?"}
    ],
    "max_tokens": 50
  }'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (200, pirate-themed) | PASS (200, echo shows system msg) | Yes |
| anthropic | PASS (200, pirate-themed) | PASS (200, echo shows system msg) | Yes |
| bedrock-openai | PASS (200) | PASS (200, echo shows system msg) | Yes |

---

## Test 4: Multi-turn Conversation

**What we test:** That multi-turn message arrays (user -> assistant -> user) are correctly
preserved through the BBR translation pipeline.

**What we verify:**
- HTTP 200
- Response references context from earlier messages (e.g., remembers the user's name)

### Curl Command

```bash
curl -sk "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<TARGET_MODEL>",
    "messages": [
      {"role": "user", "content": "My name is Alice."},
      {"role": "assistant", "content": "Hello Alice! Nice to meet you."},
      {"role": "user", "content": "What is my name?"}
    ],
    "max_tokens": 20
  }'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (200, "Your name is Alice") | PASS (200, echo shows 3 msgs) | Yes |
| anthropic | PASS (200, "Your name is Alice") | PASS (200, echo shows 3 msgs) | Yes |
| bedrock-openai | PASS (200) | PASS (200, echo shows 3 msgs) | Yes |

---

## Test 5: Tool / Function Calling

**What we test:** That OpenAI-format tool definitions are correctly translated to provider-native
format and that tool call responses are translated back to OpenAI format.

For Anthropic, the translator must:
- Convert OpenAI `tools[].function` to Anthropic `tools[]` with `input_schema`
- Convert `tool_choice: "auto"` to `{"type": "auto"}`
- Convert response `stop_reason: "tool_use"` to `finish_reason: "tool_calls"`
- Convert response `content[].type: "tool_use"` to `message.tool_calls[]`

**What we verify:**
- `finish_reason` is `tool_calls`
- `message.tool_calls[0].function.name` is `get_weather`
- `message.tool_calls[0].function.arguments` contains `location`

### Curl Command (initial tool call)

```bash
curl -sk "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<TARGET_MODEL>",
    "messages": [{"role": "user", "content": "What is the weather in San Francisco?"}],
    "tools": [{
      "type": "function",
      "function": {
        "name": "get_weather",
        "description": "Get weather for a location",
        "parameters": {
          "type": "object",
          "properties": {"location": {"type": "string", "description": "City name"}},
          "required": ["location"]
        }
      }
    }],
    "tool_choice": "auto",
    "max_tokens": 100
  }'
```

### Curl Command (tool result follow-up)

```bash
curl -sk "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<TARGET_MODEL>",
    "messages": [
      {"role": "user", "content": "What is the weather in San Francisco?"},
      {"role": "assistant", "content": null, "tool_calls": [
        {"id": "toolu_123", "type": "function", "function": {
          "name": "get_weather", "arguments": "{\"location\":\"San Francisco\"}"
        }}
      ]},
      {"role": "tool", "tool_call_id": "toolu_123", "content": "72F, sunny"}
    ],
    "tools": [{"type": "function", "function": {
      "name": "get_weather", "description": "Get weather",
      "parameters": {"type": "object", "properties": {"location": {"type": "string"}}, "required": ["location"]}
    }}],
    "max_tokens": 100
  }'
```

### Results (2026-04-19)

| Provider | Tool Call | Tool Follow-up | Simulator |
|----------|----------|----------------|-----------|
| openai | PASS (`tool_calls`, `get_weather`) | PASS (coherent response) | N/A |
| anthropic | PASS (`tool_calls`, `get_weather`) | PASS ("72F sunny" referenced) | N/A |
| bedrock-openai | Not tested | Not tested | N/A |

> **Bedrock note:** The `openai.gpt-oss-20b` model is a reasoning model that does not reliably
> produce `tool_calls` responses. The `bedrock-openai` translator is a pass-through (no body
> mutation), so tool calling would work if the underlying model supports it.
>
> **Simulator note:** The llm-katan simulator echoes requests without producing `tool_calls`
> responses, so tool calling cannot be validated against the simulator.

---

## Test 6: Error Handling — Invalid API Key

**What we test:** Requests with an invalid MaaS API key are rejected by Kuadrant (Authorino)
before reaching BBR or the provider.

### Curl Command

```bash
curl -sk -w "\nHTTP %{http_code}" \
  "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer invalid-key-12345" \
  -H "Content-Type: application/json" \
  -d '{"model":"<TARGET_MODEL>","messages":[{"role":"user","content":"hello"}]}'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (401) | PASS (401) | Yes |
| anthropic | PASS (401) | PASS (401) | Yes |
| bedrock-openai | PASS (401) | PASS (401) | Yes |

---

## Test 7: Error Handling — No Auth Header

**What we test:** Requests without any Authorization header are rejected.

### Curl Command

```bash
curl -sk -w "\nHTTP %{http_code}" \
  "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -d '{"model":"<TARGET_MODEL>","messages":[{"role":"user","content":"hello"}]}'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (401) | PASS (401) | Yes |
| anthropic | PASS (401) | PASS (401) | Yes |
| bedrock-openai | PASS (401) | PASS (401) | Yes |

> Auth is enforced at the Kuadrant layer (before BBR), so behavior is identical across all providers.

---

## Test 8: Error Handling — Model Name Mismatch

**What we test:** When the `model` field in the request body doesn't match the ExternalModel's
`targetModel`, the BBR model-provider-resolver returns an error.

### Curl Command

```bash
curl -sk -w "\nHTTP %{http_code}" \
  "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"wrong-model-name","messages":[{"role":"user","content":"hello"}]}'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (404) | PASS (404) | Yes |
| anthropic | PASS (404) | PASS (404) | Yes |
| bedrock-openai | PASS (404) | PASS (404) | Yes |

---

## Test 9: Error Handling — Non-existent Model Path

**What we test:** A request to a model path that doesn't exist returns 404 from the gateway.

### Curl Command

```bash
curl -sk -w "\nHTTP %{http_code}" \
  "https://${GATEWAY_HOST}/llm/nonexistent-model/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hello"}]}'
```

### Results (2026-04-19)

| Result | HTTP |
|--------|------|
| PASS | 404 |

---

## Test 10: Error Handling — Malformed JSON

**What we test:** A request with an invalid JSON body is rejected by BBR.

### Curl Command

```bash
curl -sk -w "\nHTTP %{http_code}" \
  "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d 'this is not json'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (400) | PASS (400) | Yes |
| anthropic | PASS (400) | PASS (400) | Yes |
| bedrock-openai | PASS (400) | PASS (400) | Yes |

---

## Test 11: Error Handling — Empty Messages Array

**What we test:** Sending an empty messages array is handled gracefully.

### Curl Command

```bash
curl -sk -w "\nHTTP %{http_code}" \
  "https://${GATEWAY_HOST}/llm/<MODEL_NAME>/v1/chat/completions" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{"model":"<TARGET_MODEL>","messages":[]}'
```

### Results (2026-04-19)

| Provider | Real | Simulator | Consistent? |
|----------|------|-----------|-------------|
| openai | PASS (400) | PASS (400) | Yes |
| anthropic | PASS (400) | PASS (400) | Yes |
| bedrock-openai | PASS (400) | PASS (400) | Yes |

> **Previously failing (fixed):** All translators returned 500 for empty messages. Fixed in
> [PR #146](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/146) by using
> `errcommon.Error{Code: BadRequest}` and preserving the error type through the plugin wrapper.
> Now returns HTTP 400 with a clear error message, matching real provider behavior (OpenAI and
> Anthropic both return 400 for empty messages).

---

## Test 12: Model Discovery

**What we test:** The `/v1/models` endpoint returns all registered ExternalModels.

### Curl Command

```bash
curl -sk "https://${GATEWAY_HOST}/maas-api/v1/models" \
  -H "Authorization: Bearer $(oc whoami -t)" | python3 -m json.tool
```

### Results (2026-04-19)

| Model ID | Kind | Ready |
|----------|------|-------|
| ext-openai | ExternalModel | True |
| ext-anthropic | ExternalModel | True |
| ext-bedrock | ExternalModel | True |
| ext-gemini | ExternalModel | True |
| sim-openai | ExternalModel | True |
| sim-anthropic | ExternalModel | True |
| sim-bedrock | ExternalModel | True |
| facebook-opt-125m-simulated | LLMInferenceService | True |

**Total models: 8** — PASS

---

## Summary

### Overall Results

| Category | Tests | Passed | Failed |
|----------|-------|--------|--------|
| Basic Chat Completions | 7 | 6 | 1 |
| Streaming (SSE) | 6 | 6 | 0 |
| System Messages | 6 | 6 | 0 |
| Multi-turn | 6 | 6 | 0 |
| Tool Calling | 2 | 2 | 0 |
| Invalid API Key | 6 | 6 | 0 |
| No Auth | 6 | 6 | 0 |
| Model Mismatch | 6 | 6 | 0 |
| Non-existent Path | 1 | 1 | 0 |
| Malformed JSON | 6 | 6 | 0 |
| Empty Messages | 6 | 6 | 0 |
| Model Discovery | 1 | 1 | 0 |
| **Total** | **59** | **58** | **1** |

**Pass Rate: 98.3%**

### Provider Coverage Matrix

| Test | OpenAI | Anthropic | Bedrock | Gemini | Sim-OpenAI | Sim-Anthropic | Sim-Bedrock |
|------|--------|-----------|---------|--------|------------|---------------|-------------|
| Basic (200) | PASS | PASS | PASS | FAIL | PASS | PASS | PASS |
| Streaming | PASS | PASS | PASS | - | PASS | PASS | PASS |
| System msg | PASS | PASS | PASS | - | PASS | PASS | PASS |
| Multi-turn | PASS | PASS | PASS | - | PASS | PASS | PASS |
| Tool calling | PASS | PASS | (1) | - | - | - | - |
| Invalid key | PASS | PASS | PASS | - | PASS | PASS | PASS |
| No auth | PASS | PASS | PASS | - | PASS | PASS | PASS |
| Model mismatch | PASS | PASS | PASS | - | PASS | PASS | PASS |
| Malformed JSON | PASS | PASS | PASS | - | PASS | PASS | PASS |
| Empty messages | PASS | PASS | PASS | - | PASS | PASS | PASS |

(1) Bedrock model (`openai.gpt-oss-20b`) is a reasoning model that doesn't reliably produce tool calls.
Bedrock-openai is a pass-through translator — tool calling works if the model supports it.

### Simulator Consistency

| Behavior | Real vs Simulator | Verdict |
|----------|-------------------|---------|
| Basic inference (200) | Both return 200 with OpenAI format | Consistent |
| Streaming (SSE) | All 3 providers stream correctly | Consistent |
| System messages | Both return 200 | Consistent |
| Multi-turn | Both return 200 | Consistent |
| Auth (invalid key) | Both return 401 | Consistent |
| Auth (no header) | Both return 401 | Consistent |
| Model mismatch | Both return 404 | Consistent |
| Malformed JSON | Both return 400 | Consistent |
| Empty messages | All return 400 | Consistent |

### Known Bugs & Gaps

| # | Issue | Severity | Status | Link |
|---|-------|----------|--------|------|
| 1 | ~~Anthropic streaming — `stream` field dropped~~ | ~~Medium~~ | **FIXED** | [PR #137](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/137) |
| 2 | ~~Empty messages returns 500~~ | ~~Low~~ | **FIXED** | [PR #146](https://github.com/opendatahub-io/ai-gateway-payload-processing/pull/146) |
| 3 | Gemini path incompatibility (`/v1beta/openai/` vs `/v1/`) | Medium | Open | [#143](https://github.com/opendatahub-io/ai-gateway-payload-processing/issues/143) |
| 4 | Azure OpenAI not tested | Info | Blocked | No API key available |
| 5 | Vertex AI not tested | Info | Blocked | No GCP service account available |

---

## Resource Setup Reference

### ExternalModel CR

```yaml
apiVersion: maas.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: <model-name>
  namespace: <ns>
spec:
  provider: <openai|anthropic|azure-openai|vertex|bedrock-openai>
  targetModel: <provider-model-id>
  endpoint: <provider-fqdn>
  credentialRef:
    name: <secret-name>
```

### Provider Secret

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: <secret-name>
  namespace: <ns>
  labels:
    inference.networking.k8s.io/bbr-managed: "true"
type: Opaque
stringData:
  api-key: "<provider-api-key>"
```

### MaaSModelRef + MaaSSubscription + MaaSAuthPolicy

```yaml
apiVersion: maas.opendatahub.io/v1alpha1
kind: MaaSModelRef
metadata:
  name: <model-name>
  namespace: <ns>
spec:
  modelRef:
    kind: ExternalModel
    name: <model-name>
---
apiVersion: maas.opendatahub.io/v1alpha1
kind: MaaSSubscription
metadata:
  name: <subscription-name>
  namespace: <maas-namespace>
spec:
  owner:
    groups:
    - name: system:authenticated
  modelRefs:
  - name: <model-name>
    namespace: <model-namespace>
    tokenRateLimits:
    - limit: 10000
      window: 1m
  priority: 100
---
apiVersion: maas.opendatahub.io/v1alpha1
kind: MaaSAuthPolicy
metadata:
  name: <policy-name>
  namespace: <maas-namespace>
spec:
  modelRefs:
  - name: <model-name>
    namespace: <model-namespace>
  subjects:
    groups:
    - name: system:authenticated
```

---

