# Payload-Processing

A chart to deploy payload-processing.

## Pre-Requisites

The target cluster must have `ExternalModel` CRD deployed.
If you're running this deployment after `model-as-a-service` the CRD is already included.
if you're running this repo as a standalone, you need to deploy the CRD before running the helm chart.

// TODO - we should pin it to a released version of upstream bbr in the next release
// TODO we should pin to odh payload processing released tag

## Install Payload Processing

1. If ExternalModel CRD is not deployed in your cluster, deploy it using the following:

    ```
    kubectl apply -f https://raw.githubusercontent.com/opendatahub-io/models-as-a-service/refs/heads/main/deployment/base/maas-controller/crd/bases/maas.opendatahub.io_externalmodels.yaml
    ```

1. Set `GATEWAY_NAME` variable that the ext proc will be attached to, e.g.,:

    ```bash
    export GATEWAY_NAME=maas-gateway
    ```

1.  Install `payload-processing` helm chart:

    ```bash
    helm install payload-processing ./payload-processing \
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
