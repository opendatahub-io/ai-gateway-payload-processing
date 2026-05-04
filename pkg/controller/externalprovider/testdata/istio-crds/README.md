# Istio CRD Stubs for envtest

These are minimal CRD definitions used by envtest to register Istio
resource types (ServiceEntry, DestinationRule) in the test API server.

envtest doesn't run a real Istio installation, so these stubs let the
reconciler create/read unstructured Istio resources during tests.

These are NOT production CRDs — they use x-kubernetes-preserve-unknown-fields
to accept any spec without validation.
