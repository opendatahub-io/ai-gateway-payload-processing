# Controller Reconcilers

Kubernetes controller reconcilers for `inference.opendatahub.io/v1alpha1` CRDs.
These run inside the `model-provider-resolver` BBR plugin and create networking
resources that route inference traffic to external LLM providers.

## ExternalProvider → Service + ServiceEntry + DestinationRule

The ExternalProvider reconciler creates three shared networking resources per provider.
Multiple ExternalModels can reference the same provider without duplicating resources.

### Input: ExternalProvider CR

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: my-openai
  namespace: llm
spec:
  provider: openai
  endpoint: api.openai.com
  auth:
    secretRef:
      name: openai-creds
  config:                    # optional, provider-specific settings
    project: my-project      # e.g., Vertex AI project/location
```

### Created: ExternalName Service

Maps an in-cluster DNS name to the external FQDN. The HTTPRoute's backend ref
points to this Service.

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-openai
  namespace: llm
  labels:
    app.kubernetes.io/managed-by: ipp-external-provider-reconciler
    inference.opendatahub.io/external-provider: my-openai
  ownerReferences:
    - apiVersion: inference.opendatahub.io/v1alpha1
      kind: ExternalProvider
      name: my-openai
      controller: true
spec:
  type: ExternalName
  externalName: api.openai.com
  ports:
    - port: 443
      targetPort: 443
```

### Created: Istio ServiceEntry

Registers the external FQDN in the Istio mesh service registry so Envoy
knows how to reach it.

```yaml
apiVersion: networking.istio.io/v1
kind: ServiceEntry
metadata:
  name: my-openai
  namespace: llm
  labels:
    app.kubernetes.io/managed-by: ipp-external-provider-reconciler
    inference.opendatahub.io/external-provider: my-openai
  ownerReferences:
    - apiVersion: inference.opendatahub.io/v1alpha1
      kind: ExternalProvider
      name: my-openai
      controller: true
spec:
  hosts:
    - api.openai.com
  location: MESH_EXTERNAL
  resolution: DNS
  ports:
    - number: 443
      name: https
      protocol: HTTPS
```

### Created: Istio DestinationRule

Configures TLS origination (mode SIMPLE) so Envoy terminates the mesh mTLS
and initiates a new TLS connection to the external provider.

```yaml
apiVersion: networking.istio.io/v1
kind: DestinationRule
metadata:
  name: my-openai
  namespace: llm
  labels:
    app.kubernetes.io/managed-by: ipp-external-provider-reconciler
    inference.opendatahub.io/external-provider: my-openai
  ownerReferences:
    - apiVersion: inference.opendatahub.io/v1alpha1
      kind: ExternalProvider
      name: my-openai
      controller: true
spec:
  host: api.openai.com
  trafficPolicy:
    tls:
      mode: SIMPLE
```

## ExternalModel → HTTPRoute

The ExternalModel reconciler creates an HTTPRoute that routes client traffic
to the provider's Service. Each model gets its own HTTPRoute with path-based
and header-based matching rules.

### Input: ExternalModel CR

```yaml
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: gpt4
  namespace: llm
spec:
  externalProviderRefs:
    - ref:
        name: my-openai          # references ExternalProvider
      targetModel: gpt-4o        # provider-side model name
      apiFormat: chat-completions # translation format
```

### Created: HTTPRoute

Two matching rules:
1. **Path prefix** (`/<namespace>/<model>`) — current routing mechanism
2. **Header match** (`X-Gateway-Model-Name`) — for unified entrypoint (future)

The backend ref points to the ExternalProvider's Service (not the model).
The Host header filter sets the SNI hostname for TLS to the external provider.

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: gpt4
  namespace: llm
  labels:
    app.kubernetes.io/managed-by: ipp-external-model-reconciler
    inference.opendatahub.io/external-model: gpt4
  ownerReferences:
    - apiVersion: inference.opendatahub.io/v1alpha1
      kind: ExternalModel
      name: gpt4
      controller: true
spec:
  parentRefs:
    - name: maas-default-gateway
      namespace: istio-system
  rules:
    # Rule 1: path-based match (current routing)
    - matches:
        - path:
            type: PathPrefix
            value: /llm/gpt4
      backendRefs:
        - name: my-openai        # provider's Service
          port: 443
      filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            set:
              - name: Host
                value: api.openai.com  # TLS SNI
      timeouts:
        request: 300s
    # Rule 2: header-based match (unified entrypoint)
    - matches:
        - headers:
            - name: X-Gateway-Model-Name
              type: Exact
              value: gpt-4o      # targetModel
      backendRefs:
        - name: my-openai
          port: 443
      filters:
        - type: RequestHeaderModifier
          requestHeaderModifier:
            set:
              - name: Host
                value: api.openai.com
      timeouts:
        request: 300s
```

## Resource Lifecycle

- **Owner references** ensure garbage collection — deleting the CR deletes all created resources.
- **Update propagation** — changing the provider endpoint updates the Service, ServiceEntry, and DestinationRule. Changing the provider also triggers re-reconciliation of dependent ExternalModels (cross-watch).
- **Status** — both CRs report `phase: Ready` when all resources are created, `phase: Failed` with a condition message on errors (missing Secret, provider not ready, etc.).
