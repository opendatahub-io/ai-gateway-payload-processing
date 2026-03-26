This is a *WIP* work to make payload processing deployment through helm chart.

### Install Payload Processing (use IGW upstream image from main)

// TODO - we should pin it to a released version in the next release

```bash
helm install payload-processing -f ./values.yaml \ 
--version v0 \
oci://us-central1-docker.pkg.dev/k8s-staging-images/gateway-api-inference-extension/charts/body-based-routing
```

### Uninstall Payload Processing Chart

```bash
helm uninstall payload-processing oci://us-central1-docker.pkg.dev/k8s-staging-images/gateway-api-inference-extension/charts/body-based-routing
```
