# ExternalProvider/ExternalModel PR Split Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Split the monolithic PR #163 into 3 reviewable PRs with clean dependencies, incorporating Nir's `Config` field feedback.

**Architecture:** Three stacked branches, each building on the previous. PR 1 is standalone (types only). PR 2 depends on PR 1 (uses the types). PR 3 depends on PR 2 (plugin reads CRDs that reconcilers manage). Each PR is independently testable at its scope level.

**Tech Stack:** Go 1.25, controller-runtime v0.23.3, controller-gen v0.16.4, gateway-api v1.5.1, envtest

---

## Pre-work: Nir's Config Field

Nir requested a free-form `Config` field on ExternalProvider for provider-specific configuration (Vertex AI project/location/endpoint, etc.). Must be added to the types in PR 1.

**Change to `externalprovider_types.go`:**
```go
type ExternalProviderSpec struct {
    Provider string     `json:"provider"`
    Endpoint string     `json:"endpoint"`
    Auth     AuthConfig `json:"auth"`
    // Config holds provider-specific configuration (e.g., Vertex AI project/location).
    // +optional
    // +kubebuilder:pruning:PreserveUnknownFields
    Config map[string]string `json:"config,omitempty"`
}
```

**Design decision:** Use `map[string]string` not `map[string]any`. Reasons:
- `map[string]any` requires `+kubebuilder:pruning:PreserveUnknownFields` AND `x-kubernetes-preserve-unknown-fields: true` in the CRD schema, which disables all validation on the map values. It also doesn't round-trip cleanly through the K8s API (numbers become float64, etc.).
- `map[string]string` is typed, validates cleanly, and covers the known use cases (project name, location, endpoint are all strings).
- If we later need nested config, we promote to a struct — same pattern as the NameReference evolution.
- **However**, if Nir explicitly wants `map[string]any` for arbitrary JSON config, use it with the pruning marker. Ask him.

**Propagation:** The Config field needs to flow through CycleState to downstream plugins (api-translation). This is a PR 3 concern — add a new state key `state.ProviderConfigKey` and write the config map to CycleState in the model reconciler.

---

## Branch Strategy

```
main
 └── feat/external-provider-types     (PR 1)
      └── feat/external-provider-reconcilers  (PR 2)
           └── feat/external-provider-plugin  (PR 3)
```

Original `feat/external-provider-crd` branch stays untouched as backup (PR #163 stays open).

Merge order: PR 1 → PR 2 → PR 3. Each PR targets `main`. After PR 1 merges, rebase PR 2 onto main. Same for PR 3.

---

## Task 1: Create PR 1 — CRD Types + Generated Artifacts + Makefile

**Scope:** 7 files, ~860 lines. The types, generated deepcopy, CRD manifests, Makefile targets.

**Files:**
- Create: `api/inference/v1alpha1/groupversion_info.go`
- Create: `api/inference/v1alpha1/externalprovider_types.go` (with Config field)
- Create: `api/inference/v1alpha1/externalmodel_types.go`
- Create: `api/inference/v1alpha1/zz_generated.deepcopy.go` (generated)
- Create: `config/crd/bases/inference.opendatahub.io_externalproviders.yaml` (generated)
- Create: `config/crd/bases/inference.opendatahub.io_externalmodels.yaml` (generated)
- Modify: `Makefile` (add generate, manifests, verify-codegen, controller-gen targets)

**Steps:**

### Step 1: Create branch from main
```bash
git checkout main
git pull origin main
git checkout -b feat/external-provider-types
```

### Step 2: Add the Config field to ExternalProvider types
Cherry-pick types from existing branch, then add Config field:
```bash
git checkout feat/external-provider-crd -- api/inference/v1alpha1/groupversion_info.go
git checkout feat/external-provider-crd -- api/inference/v1alpha1/externalmodel_types.go
git checkout feat/external-provider-crd -- api/inference/v1alpha1/externalprovider_types.go
git checkout feat/external-provider-crd -- Makefile
```
Then edit `externalprovider_types.go` to add Config field to ExternalProviderSpec.

### Step 3: Regenerate
```bash
make generate manifests
```

### Step 4: Verify
```bash
go build ./api/...
go vet ./api/...
make verify-codegen  # should show no diff
```

### Step 5: Commit and push
```bash
git add api/ config/crd/ Makefile
git commit -m "feat: add ExternalProvider and ExternalModel CRD types

Implements inference.opendatahub.io/v1alpha1 API group with two CRDs:
- ExternalProvider: provider endpoint, credentials, and provider-specific config
- ExternalModel: client-facing model name with provider ref, targetModel, apiFormat

Phase 1: MaxItems=1 on externalProviderRefs (single provider per model).
Weight field deferred to Phase 2 (traffic splitting)."
git push -u origin feat/external-provider-types
```

### Step 6: Create PR
Target: `main`. Title: `feat: ExternalProvider and ExternalModel CRD types (1/3)`

**Testing for PR 1:**
- `go build ./api/...` compiles
- `go vet ./api/...` clean
- `make generate manifests && make verify-codegen` — generated files match
- CRD YAMLs can be applied to a cluster: `kubectl apply -f config/crd/bases/ --dry-run=server`

---

## Task 2: Create PR 2 — Reconcilers + Controller Binary + Tests

**Scope:** ~2050 lines. Both reconcilers, controller binary, Dockerfile, deployment manifests, envtest tests.

**Files:**
- Create: `cmd/controller/main.go`
- Create: `Dockerfile.controller`
- Create: `deploy/controller/deployment.yaml`
- Create: `deploy/controller/samples/external-provider-katan.yaml`
- Create: `deploy/controller/samples/external-model-katan.yaml`
- Create: `pkg/controller/common/constants.go`
- Create: `pkg/controller/externalprovider/reconciler.go`
- Create: `pkg/controller/externalprovider/resources.go`
- Create: `pkg/controller/externalprovider/resources_test.go`
- Create: `pkg/controller/externalprovider/reconciler_test.go`
- Create: `pkg/controller/externalprovider/testdata/istio-crds/*.yaml`
- Create: `pkg/controller/externalmodel/reconciler.go`
- Create: `pkg/controller/externalmodel/resources.go`
- Create: `pkg/controller/externalmodel/resources_test.go`
- Create: `pkg/controller/externalmodel/reconciler_test.go`
- Create: `pkg/controller/externalmodel/testdata/gateway-api-crds/httproute-crd.yaml`

**Steps:**

### Step 1: Create branch from PR 1 branch
```bash
git checkout feat/external-provider-types
git checkout -b feat/external-provider-reconcilers
```

### Step 2: Cherry-pick reconciler code
```bash
git checkout feat/external-provider-crd -- cmd/controller/
git checkout feat/external-provider-crd -- pkg/controller/
git checkout feat/external-provider-crd -- Dockerfile.controller
git checkout feat/external-provider-crd -- deploy/controller/
```

### Step 3: Add gateway-api dependency
```bash
go get sigs.k8s.io/gateway-api@v1.5.1
```

### Step 4: Update reconcilers for Config field
The ExternalModel reconciler needs to propagate provider Config through to the CycleState or store. For PR 2, the reconciler just needs to compile against the updated types. No functional change needed — Config is optional and existing code doesn't reference it.

### Step 5: Verify
```bash
go build ./cmd/controller/
make test-unit
```
All existing tests + new envtest tests must pass.

### Step 6: Commit and push
```bash
git add cmd/ pkg/controller/ Dockerfile.controller deploy/controller/ go.mod go.sum
git commit -m "feat: ExternalProvider and ExternalModel reconcilers with controller binary

ExternalProvider reconciler creates shared Istio networking resources
(Service, ServiceEntry, DestinationRule) per provider. ExternalModel
reconciler creates HTTPRoute per model, referencing the provider's Service.

Cross-watch: provider endpoint changes trigger re-reconcile of all
referencing models. Secret validation sets Phase=Failed if missing."
git push -u origin feat/external-provider-reconcilers
```

### Step 7: Create PR
Target: `main`. Title: `feat: ExternalProvider and ExternalModel reconcilers (2/3)`
Note in description: depends on PR 1 for CRD types.

**Testing for PR 2:**
- `go build ./cmd/controller/` compiles
- `make test-unit` — all tests pass including 11 envtest integration tests
- `docker buildx build -f Dockerfile.controller .` — image builds
- Can manually test on Kind if PR 1 CRDs are applied first

---

## Task 3: Create PR 3 — BBR Plugin Integration + RBAC

**Scope:** ~620 lines. Plugin changes, store, reconcilers, tests, Helm RBAC.

**Files:**
- Modify: `pkg/plugins/model-provider-resolver/external_model_reconciler.go`
- Modify: `pkg/plugins/model-provider-resolver/plugin.go`
- Modify: `deploy/payload-processing/templates/rbac.yaml`
- Modify: `go.mod`, `go.sum` (gateway-api dep if not already added in PR 2)
- Create: `pkg/plugins/model-provider-resolver/provider_store.go`
- Create: `pkg/plugins/model-provider-resolver/provider_store_test.go`
- Create: `pkg/plugins/model-provider-resolver/external_provider_reconciler.go`
- Create: `pkg/plugins/model-provider-resolver/external_provider_reconciler_test.go`
- Create: `pkg/plugins/model-provider-resolver/external_model_reconciler_test.go`

**Steps:**

### Step 1: Create branch from PR 2 branch
```bash
git checkout feat/external-provider-reconcilers
git checkout -b feat/external-provider-plugin
```

### Step 2: Cherry-pick plugin code
```bash
git checkout feat/external-provider-crd -- pkg/plugins/model-provider-resolver/
git checkout feat/external-provider-crd -- deploy/payload-processing/templates/rbac.yaml
```

### Step 3: Add Config to CycleState (new work)
Add `ProviderConfigKey` to `pkg/plugins/common/state/state-keys.go`.
Update the ExternalModel reconciler in the plugin to write provider Config to CycleState.
Update the ExternalProvider reconciler in the plugin to store Config in providerInfoStore.

### Step 4: Verify
```bash
make test-unit
```

### Step 5: Commit and push
```bash
git add pkg/plugins/ deploy/payload-processing/ go.mod go.sum
git commit -m "feat: update model-provider-resolver plugin for new CRDs

Replace old maas.opendatahub.io watcher with inference.opendatahub.io
ExternalProvider + ExternalModel watchers. No dual-watch — only new CRDs.

Breaking change: BBR no longer resolves maas.opendatahub.io ExternalModel CRs."
git push -u origin feat/external-provider-plugin
```

### Step 6: Create PR
Target: `main`. Title: `feat: BBR plugin integration for new CRDs (3/3)`
Note in description: depends on PR 2. Breaking change — call out clearly.

**Testing for PR 3:**
- `make test-unit` — all tests pass including 15 new plugin tests
- ProcessRequest behavior unchanged (verified by existing plugin_test.go)
- Full E2E on Kind cluster with all 3 PRs applied (already done, results in PR #163)

---

## Open Question for Nir

**Config field type:** `map[string]string` or `map[string]any`?

- `map[string]string` is cleaner (typed, validates, round-trips correctly) and covers known use cases (Vertex project/location/endpoint are all strings).
- `map[string]any` is more flexible but requires `x-kubernetes-preserve-unknown-fields` and has JSON round-trip issues (numbers → float64).

Recommendation: start with `map[string]string`. If we need nested config later, promote to a typed struct.

---

## Checklist

- [ ] Ask Nir: `map[string]string` vs `map[string]any` for Config field
- [ ] Create `feat/external-provider-types` branch → PR 1
- [ ] Create `feat/external-provider-reconcilers` branch → PR 2
- [ ] Create `feat/external-provider-plugin` branch → PR 3
- [ ] Keep PR #163 open as backup
- [ ] After all 3 merge: close PR #163
