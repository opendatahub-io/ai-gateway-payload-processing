# Payload-Processing

A chart to deploy payload-processing.

<!-- TODO we should pin to odh payload processing released tag -->

## RBAC and Secrets Access

The `apikey-injection` plugin watches Kubernetes Secrets to inject provider API
keys (e.g. OpenAI, Anthropic) into outbound inference requests. Only Secrets
labeled `inference.llm-d.ai/ipp-managed: "true"` are processed.

### Multi-namespace mode (`multiNamespace: true`, the default)

A **ClusterRole** is created with `get`/`list`/`watch` on `secrets`. Kubernetes
RBAC cannot scope `secrets` by label, so static analysis tools will still flag
broad Secret access; only enable when ExternalModels and credential Secrets
actually span namespaces.

The Secret informer uses a **label selector** at list/watch time so only
`ipp-managed` Secrets enter the cache.

### Single-namespace mode (`multiNamespace: false`)

A **Role** and **RoleBinding** are created in the release namespace. This matches
[Kubernetes RBAC good practices](https://kubernetes.io/docs/concepts/security/rbac-good-practices/)
(least privilege, prefer namespace-scoped bindings) and limits exposure called
out in [#201](https://github.com/opendatahub-io/ai-gateway-payload-processing/issues/201).

Keep `ExternalModel` resources and their credential `Secret`s in the **same
namespace as the Helm release** when using this mode. If IPP runs in the
release namespace but models or credentials live in **other** namespaces, set
`upstreamIpp.payloadProcessor.multiNamespace: true` in your values (ClusterRole
path); otherwise the controller cannot read those objects.
