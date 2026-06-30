# AI Inference Gateway Demo Environment Setup

Use this prompt with Claude Code to deploy the full MaaS + AI Gateway demo stack on an OpenShift cluster with RHOAI.

---

## Prompt for Claude

```
I need to deploy the AI Inference Gateway (MaaS) demo environment on an OpenShift cluster with RHOAI. This includes MaaS platform, the Inference Payload Processor (IPP) with custom plugins, external model routing to real providers (Anthropic, OpenAI), and a metering dashboard.

## Architecture Overview

The system routes AI inference requests through an Envoy gateway with ext_proc plugins:

```
Client (Claude Code / Codex / curl)
  → Envoy Gateway (Istio, HTTPRoute)
    → Kuadrant Auth (API key validation via Authorino)
    → Pre-auth ext_proc (extracts model name from body → X-Gateway-Model-Name header)
    → Post-auth ext_proc (IPP plugin chain):
        → body-field-to-header (model name extraction)
        → maas-headers-guard (captures + strips x-maas-* headers)
        → external-metering (balance check + usage reporting)
        → model-provider-resolver (resolves model → provider from ExternalModel CRDs)
        → api-translation (format conversion between OpenAI/Anthropic/Azure/Bedrock/Vertex)
        → apikey-injection (swaps MaaS key for provider API key from K8s secret)
    → External Provider (api.anthropic.com, api.openai.com, etc.)
```

## Step 1: Deploy MaaS Platform

Clone and deploy the MaaS platform:

```bash
git clone https://github.com/opendatahub-io/models-as-a-service.git
cd models-as-a-service
./scripts/deploy.sh --operator-type rhoai
```

This deploys: ODH/RHOAI operator, Kuadrant stack (Authorino + Limitador), MaaS controller, MaaS API, Gateway, and base CRDs.

Wait for all pods in `opendatahub`, `openshift-ingress`, and `kuadrant-system` namespaces to be ready.

## Step 2: Build Custom IPP Image

The IPP (Inference Payload Processor) needs custom plugins not yet in upstream. Clone the combined-build repo and apply pending changes:

### Repos needed:

1. **ai-gateway-payload-processing** (IPP plugins):
   ```bash
   git clone https://github.com/opendatahub-io/ai-gateway-payload-processing.git
   cd ai-gateway-payload-processing
   ```

2. **llm-d-inference-payload-processor** (IPP framework):
   ```bash
   git clone https://github.com/llm-d/llm-d-inference-payload-processor.git
   ```

### Pending PRs to include:

These PRs contain features not yet merged into main. Cherry-pick or apply them:

| PR | Repo | Branch | What it adds |
|---|---|---|---|
| #169 | llm-d/llm-d-inference-payload-processor | `noyitz:feat/response-body-mode-framework` | ResponseChunkProcessor interface for streaming response processing |
| #320 | opendatahub-io/ai-gateway-payload-processing | `noyitz:feat/external-metering-dp` | External metering plugin (balance check + usage reporting) |
| #359 | opendatahub-io/ai-gateway-payload-processing | `noyitz:feat/body-name-resolver-streaming-metering` | Body-name model resolution for single-URL routing |
| #347 | opendatahub-io/ai-gateway-payload-processing | `asaadbalum:feat/issue-342-path-override` | Path field on CRDs for cross-cluster routing |

### Build approach:

The simplest way is to use the combined-build repo that merges everything:

```bash
git clone https://github.com/noyitz/ai-gateway-payload-processing.git combined-build
cd combined-build
git checkout combined-deploy-v2
```

This branch has all plugins pre-integrated. It uses a local `replace` directive in `go.mod` for the framework, so you need to:

1. Clone the framework with PR #169:
   ```bash
   git clone https://github.com/noyitz/llm-d-inference-payload-processor.git
   cd llm-d-inference-payload-processor
   git checkout feat/response-body-mode-framework
   cd ..
   ```

2. Update `go.mod` replace directive to point to your local framework path:
   ```bash
   cd combined-build
   go mod edit -replace github.com/llm-d/llm-d-inference-payload-processor=../llm-d-inference-payload-processor
   go mod vendor
   ```

3. Build and push the image:
   ```bash
   # Login to cluster registry
   REGISTRY="default-route-openshift-image-registry.apps.<YOUR_CLUSTER_DOMAIN>"
   oc whoami -t | docker login $REGISTRY -u $(oc whoami) --password-stdin

   # Build for linux/amd64
   docker build --platform linux/amd64 --no-cache --provenance=false \
     --build-arg CGO_ENABLED=0 \
     -t ${REGISTRY}/openshift-ingress/payload-processing-test:latest .

   docker push ${REGISTRY}/openshift-ingress/payload-processing-test:latest
   ```

4. Deploy the custom image:
   ```bash
   oc set image deploy/payload-processing -n openshift-ingress \
     payload-processing=image-registry.openshift-image-registry.svc:5000/openshift-ingress/payload-processing-test:latest
   ```

## Step 3: Set Environment Variables on IPP

The reconciler needs to know the gateway name:

```bash
oc set env deploy/payload-processing -n openshift-ingress \
  GATEWAY_NAME=maas-default-gateway \
  GATEWAY_NAMESPACE=openshift-ingress
```

## Step 4: Create Provider Secrets

Create Kubernetes secrets with your provider API keys. Replace the values with your actual keys:

```bash
# Anthropic
oc create secret generic anthropic-api-key -n llm \
  --from-literal=api-key='YOUR_ANTHROPIC_API_KEY'

# OpenAI
oc create secret generic openai-api-key -n llm \
  --from-literal=api-key='YOUR_OPENAI_API_KEY'
```

If secrets already exist but aren't picked up, touch them to trigger reconciliation:
```bash
oc annotate secret anthropic-api-key openai-api-key -n llm touched="$(date -u +%s)" --overwrite
```

## Step 5: Create ExternalProvider CRDs

```bash
# Anthropic provider
oc apply -f - <<'EOF'
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: ext-anthropic
  namespace: llm
spec:
  provider: anthropic
  endpoint: api.anthropic.com
  auth:
    type: simple
    secretRef:
      name: anthropic-api-key
EOF

# OpenAI provider
oc apply -f - <<'EOF'
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: ext-openai
  namespace: llm
spec:
  provider: openai
  endpoint: api.openai.com
  auth:
    type: simple
    secretRef:
      name: openai-api-key
EOF
```

## Step 6: Create ExternalModel CRDs

Each model maps a client-facing model name to a provider:

```bash
# Claude Opus
oc apply -f - <<'EOF'
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: ext-opus
  namespace: llm
spec:
  modelName: claude-opus-4-8
  externalProviderRefs:
  - ref:
      name: ext-anthropic
    targetModel: claude-opus-4-8
    apiFormat: messages
    path: /v1/messages
    weight: 1
EOF

# Claude Sonnet
oc apply -f - <<'EOF'
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: ext-sonnet
  namespace: llm
spec:
  modelName: claude-sonnet-4-6
  externalProviderRefs:
  - ref:
      name: ext-anthropic
    targetModel: claude-sonnet-4-6
    apiFormat: messages
    path: /v1/messages
    weight: 1
EOF

# Claude Haiku
oc apply -f - <<'EOF'
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: ext-haiku
  namespace: llm
spec:
  modelName: claude-haiku-4-5-20251001
  externalProviderRefs:
  - ref:
      name: ext-anthropic
    targetModel: claude-haiku-4-5-20251001
    apiFormat: messages
    path: /v1/messages
    weight: 1
EOF

# GPT-5.5
oc apply -f - <<'EOF'
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: ext-openai
  namespace: llm
spec:
  modelName: gpt-5.5
  externalProviderRefs:
  - ref:
      name: ext-openai
    targetModel: gpt-5.5
    apiFormat: openai-responses
    path: /v1/responses
    weight: 1
EOF
```

## Step 7: Create Istio Networking Resources

External providers need ServiceEntry + DestinationRule for TLS:

```bash
# Anthropic
oc apply -f - <<'EOF'
apiVersion: networking.istio.io/v1alpha3
kind: ServiceEntry
metadata:
  name: ext-anthropic
  namespace: llm
spec:
  hosts: ["api.anthropic.com"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - number: 443
    name: https
    protocol: HTTPS
---
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
  name: ext-anthropic
  namespace: llm
spec:
  host: api.anthropic.com
  trafficPolicy:
    tls:
      mode: SIMPLE
EOF

# OpenAI
oc apply -f - <<'EOF'
apiVersion: networking.istio.io/v1alpha3
kind: ServiceEntry
metadata:
  name: ext-openai
  namespace: llm
spec:
  hosts: ["api.openai.com"]
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
  - number: 443
    name: https
    protocol: HTTPS
---
apiVersion: networking.istio.io/v1alpha3
kind: DestinationRule
metadata:
  name: ext-openai
  namespace: llm
spec:
  host: api.openai.com
  trafficPolicy:
    tls:
      mode: SIMPLE
EOF
```

Also create ExternalName Services for the HTTPRoute backends:
```bash
for provider in ext-anthropic ext-openai; do
  ENDPOINT=$(oc get externalprovider $provider -n llm -o jsonpath='{.spec.endpoint}')
  oc apply -f - <<EOF
apiVersion: v1
kind: Service
metadata:
  name: $provider
  namespace: llm
spec:
  type: ExternalName
  externalName: $ENDPOINT
  ports:
  - port: 443
    protocol: TCP
EOF
done
```

## Step 8: Apply Required Patches

### 8a. EnvoyFilter: FULL_DUPLEX_STREAMED mode

The MaaS controller sets `response_body_mode: STREAMED` which breaks streaming. Fix it:

```bash
oc get envoyfilter payload-processing -n openshift-ingress -o json | \
  python3 -c "
import sys, json
ef = json.load(sys.stdin)
for p in ef['spec']['configPatches']:
    pm = p.get('patch',{}).get('value',{}).get('typed_config',{}).get('processing_mode',{})
    if 'response_body_mode' in pm:
        pm['response_body_mode'] = 'FULL_DUPLEX_STREAMED'
json.dump(ef, sys.stdout)
" | oc apply -f -
```

### 8b. Restart gateway pod to pick up EnvoyFilter changes:

```bash
oc delete pod -n openshift-ingress \
  -l gateway.networking.k8s.io/gateway-name=maas-default-gateway
```

### 8c. Lua filter for x-api-key → Authorization:Bearer conversion

Claude Code sends `x-api-key` header but Kuadrant expects `Authorization: Bearer`. Add a Lua filter:

```bash
oc apply -f - <<'EOF'
apiVersion: networking.istio.io/v1alpha3
kind: EnvoyFilter
metadata:
  name: xapikey-to-bearer
  namespace: openshift-ingress
spec:
  workloadSelector:
    labels:
      gateway.networking.k8s.io/gateway-name: maas-default-gateway
  configPatches:
  - applyTo: HTTP_FILTER
    match:
      context: GATEWAY
      listener:
        filterChain:
          filter:
            name: envoy.filters.network.http_connection_manager
            subFilter:
              name: extensions.istio.io/wasmplugin/openshift-ingress.kuadrant-maas-default-gateway
    patch:
      operation: INSERT_BEFORE
      value:
        name: envoy.filters.http.lua.xapikey
        typed_config:
          '@type': type.googleapis.com/envoy.extensions.filters.http.lua.v3.Lua
          default_source_code:
            inline_string: |
              function envoy_on_request(handle)
                local method = handle:headers():get(":method")
                if method == "HEAD" then
                  handle:respond({[":status"] = "200"}, "")
                  return
                end
                local xkey = handle:headers():get("x-api-key")
                if xkey and not handle:headers():get("authorization") then
                  handle:headers():add("authorization", "Bearer " .. xkey)
                end
              end
EOF
```

### 8d. URLRewrite on HTTPRoutes

All HTTPRoutes need URLRewrite to strip the path prefix before sending to providers:

```bash
for route in $(oc get httproutes -n llm -o jsonpath='{.items[*].metadata.name}'); do
  RULES=$(oc get httproute $route -n llm -o jsonpath='{range .spec.rules[*]}{.matches[0].path.value}{"\n"}{end}' | wc -l)
  for i in $(seq 0 $(($RULES - 1))); do
    oc patch httproute $route -n llm --type=json \
      -p "[{\"op\": \"add\", \"path\": \"/spec/rules/$i/filters/-\", \"value\": {\"type\": \"URLRewrite\", \"urlRewrite\": {\"path\": {\"type\": \"ReplacePrefixMatch\", \"replacePrefixMatch\": \"/\"}}}}]" 2>/dev/null
  done
done
```

## Step 9: Update IPP Config

Apply the IPP plugin chain configuration:

```bash
oc apply -f - <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: ipp-config
  namespace: openshift-ingress
data:
  config.yaml: |
    plugins:
    - name: model-extractor
      parameters:
        fieldName: model
        headerName: X-Gateway-Model-Name
      type: body-field-to-header
    - name: maas-headers-guard
      type: maas-headers-guard
    - name: metering-streaming
      parameters:
        failOpen: true
        featureKey: inference-tokens
        meteringURL: http://metering-service.openshift-ingress.svc:8080
        source: maas-gateway
        timeoutSeconds: 5
      type: external-metering-streaming
    - name: model-provider-resolver
      type: model-provider-resolver
    - name: api-translation
      type: api-translation
    - name: apikey-injection
      type: apikey-injection
    profiles:
    - name: default
      plugins:
        request:
        - pluginRef: model-extractor
        - pluginRef: maas-headers-guard
        - pluginRef: metering-streaming
        - pluginRef: model-provider-resolver
        - pluginRef: api-translation
        - pluginRef: apikey-injection
        response:
        - pluginRef: metering-streaming
EOF
```

Restart IPP to pick up config:
```bash
oc rollout restart deploy/payload-processing -n openshift-ingress
```

## Step 10: Deploy Metering Dashboard (Optional)

```bash
git clone https://github.com/noyitz/ai-gateway-metering-service.git
cd ai-gateway-metering-service

# Build and push
REGISTRY="default-route-openshift-image-registry.apps.<YOUR_CLUSTER_DOMAIN>"
docker build --platform linux/amd64 --no-cache --provenance=false \
  -t ${REGISTRY}/openshift-ingress/metering-dashboard:latest .
docker push ${REGISTRY}/openshift-ingress/metering-dashboard:latest

# Deploy
oc apply -f - <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: metering-service
  namespace: openshift-ingress
spec:
  replicas: 1
  selector:
    matchLabels:
      app: metering-service
  template:
    metadata:
      labels:
        app: metering-service
    spec:
      containers:
      - name: metering
        image: image-registry.openshift-image-registry.svc:5000/openshift-ingress/metering-dashboard:latest
        ports:
        - containerPort: 8080
        env:
        - name: NAMESPACE
          value: llm
        - name: CONFIG_NAMESPACE
          value: openshift-ingress
---
apiVersion: v1
kind: Service
metadata:
  name: metering-service
  namespace: openshift-ingress
spec:
  selector:
    app: metering-service
  ports:
  - port: 8080
    targetPort: 8080
EOF
```

Expose the dashboard via route:
```bash
oc create route edge metering-dashboard \
  --service=metering-service \
  --port=8080 \
  -n openshift-ingress
```

## Step 11: Generate MaaS API Keys

Port-forward to the MaaS API and create keys:

```bash
# Find the maas-api pod
oc port-forward -n opendatahub svc/maas-api 8443:8443 &

# Create a user API key
curl -sk https://localhost:8443/v1/api-keys \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-MaaS-Username: YOUR_USERNAME" \
  -H 'X-MaaS-Group: ["ai-eng"]' \
  -H "X-MaaS-Tenant: models-as-a-service" \
  -d '{"name":"my-key"}'
```

The response contains your MaaS API key (format: `sk-oai-{keyID}_{secret}`). Save it.

## Step 12: Test with curl

Get the gateway URL:
```bash
GATEWAY_URL=$(oc get gateway maas-default-gateway -n openshift-ingress \
  -o jsonpath='{.status.addresses[0].value}')
# Or use the route:
GATEWAY_URL="https://maas.apps.<YOUR_CLUSTER_DOMAIN>"
```

Test Anthropic (single-URL, model in body):
```bash
curl -sk $GATEWAY_URL/v1/messages \
  -H "Content-Type: application/json" \
  -H "x-api-key: YOUR_MAAS_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -d '{
    "model": "claude-opus-4-8",
    "messages": [{"role": "user", "content": "Hello!"}],
    "max_tokens": 50
  }'
```

Test OpenAI:
```bash
curl -sk $GATEWAY_URL/v1/responses \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_MAAS_API_KEY" \
  -d '{
    "model": "gpt-5.5",
    "input": "Hello!",
    "max_output_tokens": 50
  }'
```

## Step 13: Configure Claude Code

To use Claude Code through the gateway:

```bash
ANTHROPIC_BASE_URL=https://maas.apps.<YOUR_CLUSTER_DOMAIN>/v1 \
ANTHROPIC_API_KEY=YOUR_MAAS_API_KEY \
claude
```

Or for per-model URL pattern:
```bash
ANTHROPIC_BASE_URL=https://maas.apps.<YOUR_CLUSTER_DOMAIN>/llm/ext-opus \
ANTHROPIC_API_KEY=YOUR_MAAS_API_KEY \
claude
```

For Codex:
```bash
OPENAI_BASE_URL=https://maas.apps.<YOUR_CLUSTER_DOMAIN>/llm/ext-openai \
OPENAI_API_KEY=YOUR_MAAS_API_KEY \
codex
```

## Troubleshooting

### Common issues:

1. **503 upstream_reset_before_response_started**: Gateway pod needs restart after EnvoyFilter changes.

2. **Empty response body (HTTP 200, 0 bytes)**: EnvoyFilter `response_body_mode` is wrong. Must be `FULL_DUPLEX_STREAMED`, not `STREAMED`. Re-apply step 8a and restart gateway.

3. **401 auth errors**: Check that the Lua filter (step 8c) is in place for x-api-key conversion. Verify the MaaS API key is valid.

4. **404 on requests**: Check HTTPRoute rules exist and have the correct gateway parentRef name (`maas-default-gateway`).

5. **Secrets not picked up**: Annotate secrets to trigger reconciliation (step 4).

6. **Model not found (passthrough)**: Check `spec.modelName` on ExternalModel matches what the client sends in the body `model` field.

### Useful debug commands:

```bash
# Check IPP logs
oc logs -n openshift-ingress -l app=payload-processing --tail=50

# Check envoy access logs
GW_POD=$(oc get pods -n openshift-ingress \
  -l gateway.networking.k8s.io/gateway-name=maas-default-gateway \
  -o jsonpath='{.items[0].metadata.name}')
oc logs $GW_POD -n openshift-ingress -c istio-proxy --tail=20

# Check CRD state
oc get externalmodels.inference.opendatahub.io -n llm
oc get externalproviders.inference.opendatahub.io -n llm
oc get httproutes -n llm

# Check IPP config
oc get cm ipp-config -n openshift-ingress -o yaml
```
```
