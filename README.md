# AI Gateway Payload Processing

This repository contains Payload Processing plugins that will be connected to an AI Gateway via a pluggable BBR (Body Based Routing) framework developed as part of the [Kubernetes Inference Gateway](https://github.com/kubernetes-sigs/gateway-api-inference-extension).

BBR plugins enable custom request/response mutations of both headers and body, allowing advanced capabilities such as promoting the model from a field in the body to a header and routing to a selected endpoint accordingly.

## Pre-Requisites

The target cluster must have `ExternalModel` CRD deployed.  
If you're running this deployment after `model-as-a-service`, the CRD is already included.
if you're running this repo as a standalone, you need to deploy the CRD before running the helm chart.

<!-- TODO we should pin it to a released version of upstream bbr in the next release -->

<!-- TODO we should pin to odh payload processing released tag -->

## Install Payload Processing

1. If ExternalModel CRD is not deployed in your cluster, deploy it using the following:

    ```bash
    kubectl apply -f https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml
    ```

1. Set `GATEWAY_NAME` variable that the ext proc will be attached to, e.g.,:

    ```bash
    export GATEWAY_NAME=maas-gateway
    ```

1.  Install `payload-processing` helm chart:

    ```bash
    helm install payload-processing ./deploy/payload-processing \
    --dependency-update \
    --set upstreamBbr.inferenceGateway.name=${GATEWAY_NAME}
    ```

## Cleanup

1.  Uninstall `payload-processing` helm chart:

    ```bash
    helm uninstall payload-processing
    ```

1.  Delete the ExternalModel CRD (optionally):

    ```bash
    kubectl delete -f https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml
    ```
