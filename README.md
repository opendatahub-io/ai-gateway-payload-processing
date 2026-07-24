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
    --set upstreamIpp.payloadProcessor.env[1].value=${GATEWAY_NAMESPACE}
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

## Cleanup

1.  Uninstall `payload-processing` helm chart:

    ```bash
    helm uninstall payload-processing --namespace ${GATEWAY_NAMESPACE}
    ```

1.  Delete the CRDs (optionally):

    ```bash
    kubectl delete -f config/crd/bases/
    ```
