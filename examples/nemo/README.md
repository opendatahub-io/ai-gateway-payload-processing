# NeMo Guardrails with IPP

Guide for wiring [NeMo Guardrails](https://github.com/NVIDIA/NeMo-Guardrails)
to IPP's `nemo-request-guard` and `nemo-response-guard` plugins.

The plugins work with any NeMo Guardrails deployment that serves the
`/v1/guardrail/checks` response schema. The endpoint URL is fully configurable
via the `nemoURL` parameter.

## Overview

IPP provides two NeMo plugins:

| Plugin | Direction | What it checks |
|--------|-----------|----------------|
| `nemo-request-guard` | Input rails | User messages **before** they reach the model |
| `nemo-response-guard` | Output rails | Model responses **before** they reach the caller |

Both plugins POST messages to NeMo's `/v1/guardrail/checks` endpoint and inspect
the `status` field in the response. The status values align with NeMo's internal
`RailStatus` enum.

| Status | Behavior | HTTP |
|--------|----------|------|
| `passed` | Request/response proceeds normally | 200 |
| `modified` | Content was redacted by NeMo (e.g. PII masked) - the plugin does not support redaction yet, so the request/response passes through with the original content | 200 |
| `blocked` | Blocked - returns error to the client | 403 |

## Prerequisites

- Kubernetes cluster with `kubectl` configured
- The `nemo-request-guard` and/or `nemo-response-guard` plugins built into your IPP image

## Step 1: Deploy NeMo Guardrails

Deploy a NeMo Guardrails instance on Kubernetes.

This example uses the
[TrustyAI NeMo Guardrails Helm chart](https://github.com/trustyai-explainability/trustyai-llm-demo/tree/add-mcp-guardrails/mcp-guardrails/deploy),
which provides a preconfigured setup with the enhanced guardrail checks endpoint.

### Example NeMo Configuration

Below is a sample ConfigMap with PII detection on input. The NeMo configuration
controls what gets checked - the plugin only reads the `status` from the response.

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: nemo-config
data:
  config.yaml: |
    colang_version: "1.0"
    rails:
      config:
        sensitive_data_detection:
          input:
            entities:
              - PERSON
              - EMAIL_ADDRESS
              - PHONE_NUMBER
              - CREDIT_CARD
              - US_SSN
            score_threshold: 0.5
      input:
        flows:
          - detect sensitive data on input

  rails.co: |
    define bot refuse to respond
      "Please do not share personal or sensitive information in your message."

    define subflow detect sensitive data on input
      $has_sensitive_data = execute detect_sensitive_data(source="input", text=$user_message)
      if $has_sensitive_data
        bot refuse to respond
        stop
```

> **Note:** What entities to detect, what to block, and what to redact is entirely
> a NeMo configuration concern. The plugin does not perform any content analysis
> itself. See the [NeMo YAML schema](https://docs.nvidia.com/nemo/guardrails/latest/configure-rails/yaml-schema/index.html)
> for all available options.

## Step 2: Verify NeMo is Working

Port-forward to the NeMo pod and test with `curl`:

```bash
kubectl port-forward pod/${NEMO_POD} 8000:8000 -n ${GUARDRAILS_NS}
```

**Allowed request** (no PII):

```bash
curl -s http://localhost:8000/v1/guardrail/checks \
  -H "Content-Type: application/json" \
  -d '{
    "model": "",
    "messages": [{"role": "user", "content": "What is 2+2?"}]
  }' | jq .
```

Expected: `"passed"`

**Blocked request** (PII detected):

```bash
curl -s http://localhost:8000/v1/guardrail/checks \
  -H "Content-Type: application/json" \
  -d '{
    "model": "",
    "messages": [{"role": "user", "content": "My email is john@example.com"}]
  }' | jq .
```

Expected: `"blocked"`

## Step 3: Configure the IPP Plugins

Add one or both NeMo guard plugins to IPP's deployment args. The `--plugin` format
is `<type>:<name>:<json-config>`:

### Request Guard (input rails)

```text
--plugin nemo-request-guard:nemo-input:{"nemoURL":"http://nemo-guardrails.nemo-guardrails.svc:8000/v1/guardrail/checks","timeoutSeconds":10}
```

### Response Guard (output rails)

```text
--plugin nemo-response-guard:nemo-output:{"nemoURL":"http://nemo-guardrails.nemo-guardrails.svc:8000/v1/guardrail/checks","timeoutSeconds":10}
```

### Configuration Fields

Both plugins share the same configuration schema:

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `nemoURL` | Yes | - | Full URL to POST guardrail check requests to (e.g. `http://nemo:8000/v1/guardrail/checks`). The plugin expects the `/v1/guardrail/checks` response schema but the URL itself is fully configurable. |
| `timeoutSeconds` | No | `360` | How long IPP waits for a NeMo response |

The `nemoURL` is the **full endpoint URL** - the plugin POSTs directly to this URL
without appending any path.

### IPP Deployment Example

Add the plugin flags to your IPP deployment args:

```yaml
containers:
  - name: bbr
    args:
      - "--plugin"
      - "nemo-request-guard:nemo-input:{\"nemoURL\":\"http://nemo-guardrails.nemo-guardrails.svc:8000/v1/guardrail/checks\",\"timeoutSeconds\":10}"
      - "--plugin"
      - "nemo-response-guard:nemo-output:{\"nemoURL\":\"http://nemo-guardrails.nemo-guardrails.svc:8000/v1/guardrail/checks\",\"timeoutSeconds\":10}"
```

### ext_proc Note for Response Guard

If using `nemo-response-guard`, make sure your Envoy ext_proc configuration has
`response_body_mode: FULL_DUPLEX_STREAMED` and `response_header_mode: SEND` in the
`processing_mode` section. Without this, Envoy will not forward response bodies
to IPP and the response guard will silently skip processing.

## How It Works

```text
User -> Gateway -> IPP -> nemo-request-guard -> NeMo (input rails) -> Model Backend
                                                                           |
                                                                      Model response
                                                                           |
User <- Gateway <- IPP <- nemo-response-guard <- NeMo (output rails) <----+

  NeMo status:
    "passed"   -> forward request / return response
    "modified" -> forward (original content, redaction not yet applied)
    "blocked"  -> HTTP 403
```

1. The user sends a request to the Gateway, which forwards it to IPP.
2. IPP runs the `nemo-request-guard` plugin, which extracts all messages from the
   request body and POSTs them to the configured NeMo endpoint.
3. NeMo runs all configured **input** rails (PII detection, keyword blocking, etc.)
   and returns a JSON response with a top-level `status` field.
4. The plugin inspects `status`:
   - `"passed"` or `"modified"` - the request proceeds to the model backend.
   - `"blocked"` - IPP returns HTTP 403.
   - Any unknown status - IPP returns HTTP 500 (fail-closed).
5. After the model responds, IPP runs the `nemo-response-guard` plugin.
6. The response guard extracts assistant messages from all `choices` in the
   response (checking `message.content` first, falling back to `delta.content`
   for streaming) and POSTs them to NeMo.
7. NeMo runs all configured **output** rails and the same status logic applies.
8. The response flows back through IPP and the Gateway to the user.

The plugins also support MCP JSON-RPC payloads - string arguments from
`params.arguments` are extracted and sent to NeMo as a user message.

## References

- [TrustyAI NeMo Guardrails Helm Chart](https://github.com/trustyai-explainability/trustyai-llm-demo/tree/add-mcp-guardrails/mcp-guardrails/deploy) - deployment guide
- [NVIDIA NeMo Guardrails](https://github.com/NVIDIA/NeMo-Guardrails)
