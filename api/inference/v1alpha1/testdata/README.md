# Inference schema fixture

`ai-gateway-controller` owns the inference API. `agc-schema.json` is the
validation schema it generates under `config/crd/bases`, with descriptions
removed so documentation changes do not hide API drift. `TestAGCSchemaMirror`
compares the CRDs generated in this repo with it.

To mirror an agreed API change, copy AGC's `common_types.go`,
`externalmodel_types.go` and `externalprovider_types.go`, run
`make generate manifests`, and regenerate the fixture from AGC's
`config/crd/bases`:

```sh
yq -n -o=json '{
  "externalmodels": load("inference.opendatahub.io_externalmodels.yaml"),
  "externalproviders": load("inference.opendatahub.io_externalproviders.yaml")
} | map_values(.spec.versions[0].schema.openAPIV3Schema)
  | del(.. | select(type == "!!map") | .description | select(type == "!!str"))' \
  > <this-repo>/api/inference/v1alpha1/testdata/agc-schema.json
```
