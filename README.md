# AI Gateway Payload Processing

This repository contains Payload Processing plugins that will be connected to an AI Gateway via a pluggable IPP (Inference Payload Processor) framework developed as part of [llm-d](https://github.com/llm-d/llm-d-inference-payload-processor).

IPP plugins enable custom request/response mutations of both headers and body, allowing advanced capabilities such as promoting the model from a field in the body to a header and routing to a selected endpoint accordingly.

## Pre-Requisites

The target cluster must have `ExternalModel` and `ExternalProvider` CRDs deployed.  

```bash
kubectl apply -f config/crd/bases/
```

## Install Payload Processing

1. Set `GATEWAY_NAME` and `GATEWAY_NAMESPACE` variables. The chart **must be
   installed in the same namespace as the Gateway** for the Istio EnvoyFilter
   `targetRefs` to work:

    ```bash
    export GATEWAY_NAME=maas-default-gateway
    export GATEWAY_NAMESPACE=openshift-ingress
    export CLUSTER_NAME=cluster-a   # optional; leave unset to disable loop detection
    ```

1.  Clean local copy of upstream chart to avoid using stale version:

    ```bash
    rm -f ./deploy/payload-processing/charts/payload-processor-*.tgz
    ```

1.  Install `payload-processing` helm chart:

    ```bash
    helm install payload-processing ./deploy/payload-processing \
    --namespace ${GATEWAY_NAMESPACE} \
    --dependency-update \
    --set upstreamIpp.inferenceGateway.name=${GATEWAY_NAME} \
    --set upstreamIpp.provider.istio.envoyFilter.operation=INSERT_AFTER \
    --set upstreamIpp.provider.istio.envoyFilter.anchorSubFilter=extensions.istio.io/wasmplugin/${GATEWAY_NAMESPACE}.kuadrant-${GATEWAY_NAME} \
    --set upstreamIpp.payloadProcessor.env[0].name=GATEWAY_NAME \
    --set upstreamIpp.payloadProcessor.env[0].value=${GATEWAY_NAME} \
    --set upstreamIpp.payloadProcessor.env[1].name=GATEWAY_NAMESPACE \
    --set upstreamIpp.payloadProcessor.env[1].value=${GATEWAY_NAMESPACE} \
    --set upstreamIpp.payloadProcessor.env[2].name=CLUSTER_NAME \
    --set upstreamIpp.payloadProcessor.env[2].value=${CLUSTER_NAME}
    ```

    > **Important**: The payload processing ext proc is attached to a Gateway.
    > As a mandatory requirement, `--namespace` must match the namespace where the
    > Gateway resource lives.

    > **Timeouts**: The Envoy ext_proc filter requires explicit `message_timeout`
    > and `grpc_service.timeout` settings. Without them, Envoy uses very short
    > defaults that cause timeout errors on large-context streaming requests
    > (where Time To First Token exceeds the default). Set both to `300s` to
    > match the HTTPRoute request timeout.

    The `GATEWAY_NAME` and `GATEWAY_NAMESPACE` environment variables are used by
    the controller reconcilers to set the correct parent ref on HTTPRoutes created
    for ExternalModel CRs.

    `CLUSTER_NAME` is this cluster's identity for routing loop detection
    (issue #343). Incoming `X-Origin-Cluster` matching this value is rejected
    with HTTP 403. Leave it unset (or empty) to disable loop detection. Do not
    put `CLUSTER_NAME` in `values.yaml` as `env[0]` — Helm `--set env[0]` from
    the install command would replace the whole list. To route requests to a
    model hosted on another RHOAI/ODH cluster, set `CLUSTER_NAME` on both
    clusters and follow the [remote MaaS cluster guide](docs/providers/remote-maas.md).

    ### gRPC max receive message size

    Envoy delivers each ext_proc request/response body chunk as one gRPC message.
    The processor's **per-message** receive limit defaults to **32 MiB** (above
    gRPC's compiled 4 MiB default) so multimodal / agent chat bodies with base64
    images do not fail with an empty HTTP 500 (`received_message_larger_than_max`).

    For Helm installs, append these arguments to the `helm install` command above
    to override `GRPC_MAX_RECV_MSG_SIZE` (bytes or a Kubernetes quantity such as
    `64Mi`):

    ```bash
    --set upstreamIpp.payloadProcessor.env[3].name=GRPC_MAX_RECV_MSG_SIZE \
    --set upstreamIpp.payloadProcessor.env[3].value=64Mi
    ```

    When MaaS owns the Deployment, this env override is **not currently
    supported**. A one-off `kubectl set env` will be reconciled away, and there
    is not yet a `MaasTenantConfig` field or annotation to persist it. Tracked
    as [RHOAIENG-102134](https://redhat.atlassian.net/browse/RHOAIENG-102134).

## Cleanup

1.  Uninstall `payload-processing` helm chart:

    ```bash
    helm uninstall payload-processing --namespace ${GATEWAY_NAMESPACE}
    ```

1.  Delete the CRDs (optionally):

    ```bash
    kubectl delete -f config/crd/bases/
    ```
