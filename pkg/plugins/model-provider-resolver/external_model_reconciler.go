/*
Copyright 2026 The opendatahub.io Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model_provider_resolver

import (
	"context"
	"fmt"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	ctrlcommon "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/common"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
)

const providerRequeueDelay = 5 * time.Second

// externalModelReconciler watches inference.opendatahub.io ExternalModel CRDs
// and resolves provider info from the provider store.
type externalModelReconciler struct {
	client.Reader
	store *infoStore
}

func (r *externalModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	logger.Info("reconciling ExternalModel", "name", req.Name, "namespace", req.Namespace)

	model := &inferencev1alpha1.ExternalModel{}
	err := r.Get(ctx, req.NamespacedName, model)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get ExternalModel: %w", err)
	}

	if errors.IsNotFound(err) || !model.GetDeletionTimestamp().IsZero() {
		r.store.deleteModel(req.NamespacedName)
		logger.Info("ExternalModel removed from store", "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, nil
	}

	// RHOAIENG-99761: AGC attachments (gatewayRefs) are served by
	// ai-gateway-controller, and a foreign-only reference set is unservable
	// by IPP (AGC skips cross-namespace refs too, RHOAIENG-90804). So are the
	// models AGC serves through their own Praxis tenant (RHOAIENG-102557). In
	// every case IPP has nothing to serve: stop any previously cached mapping
	// (a legacy object edited to either shape, or a model cached before AGC
	// served it) and never add such models to the store; a same-named model
	// AGC serves must not displace a tenant model's mapping.
	if excludedFromStore(model) {
		removed := r.store.deleteModel(req.NamespacedName)
		logger.Info("ExternalModel served by AGC excluded from store", "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, r.restoreModelNames(ctx, removed)
	}

	return r.cacheModel(ctx, model), nil
}

// excludedFromStore reports whether AGC, not IPP, serves the model (see
// Reconcile).
func excludedFromStore(model *inferencev1alpha1.ExternalModel) bool {
	return ctrlcommon.IsAGCManaged(model) || ctrlcommon.IsAGCServed(model)
}

// clientModelName is the client-facing name the store keys the model by.
func clientModelName(model *inferencev1alpha1.ExternalModel) string {
	if model.Spec.ModelName != "" {
		return model.Spec.ModelName
	}
	return model.Name
}

// restoreModelNames maps each modelName an excluded model held to another
// ExternalModel of that name. The excluded model, cached before AGC served
// it, displaced that model's mapping, which would otherwise stay missing until
// the model is reconciled again.
func (r *externalModelReconciler) restoreModelNames(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}
	models := &inferencev1alpha1.ExternalModelList{}
	if err := r.List(ctx, models); err != nil {
		return fmt.Errorf("unable to list ExternalModels to restore %v: %w", names, err)
	}
	for i := range models.Items {
		model := &models.Items[i]
		if !slices.Contains(names, clientModelName(model)) || !model.GetDeletionTimestamp().IsZero() || excludedFromStore(model) {
			continue
		}
		r.cacheModel(ctx, model)
	}
	return nil
}

// cacheModel stores the model's mapping with the refs whose providers are
// available in the store.
func (r *externalModelReconciler) cacheModel(ctx context.Context, model *inferencev1alpha1.ExternalModel) ctrl.Result {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	modelName := clientModelName(model)

	// Resolve all refs whose providers are available in the store.
	var resolved []*resolvedProviderRef
	for i := range model.Spec.ExternalProviderRefs {
		ref := &model.Spec.ExternalProviderRefs[i]
		resolvedRef, err := r.resolveRef(model.Namespace, ref)
		if err != nil {
			logger.Error(err, "failed to resolve ref, skipping", "provider", model.Spec.ExternalProviderRefs[i].Ref.Name)
			continue
		}
		resolved = append(resolved, resolvedRef)
	}

	if len(resolved) == 0 {
		// A mixed-shape model whose local references all fail resolution
		// keeps its last-known-good mapping, exactly like a local-only
		// model: the shape is servable, so this is a transient readiness
		// failure rather than a handoff.
		logger.Info("no ExternalProvider available for any ref, requeuing")
		return ctrl.Result{RequeueAfter: providerRequeueDelay}
	}

	r.store.addOrUpdateModel(modelName, &externalModelInfo{owner: client.ObjectKeyFromObject(model), modelName: modelName, refs: resolved})
	logger.Info("updated model store", "modelName", modelName, "resolvedRefs", len(resolved))
	return ctrl.Result{}
}

// resolveRef resolves a single ExternalProviderRef to provider info.
// Cross-namespace references are never resolved by IPP: a foreign-only
// reference set is AGC-managed and excluded before this runs
// (RHOAIENG-99761), so this error can only surface for a mixed-shape model,
// whose remaining local references are resolved independently of the
// skipped foreign one.
func (r *externalModelReconciler) resolveRef(namespace string, ref *inferencev1alpha1.ExternalProviderRef) (*resolvedProviderRef, error) {
	if ref.Ref.Namespace != "" && ref.Ref.Namespace != namespace {
		return nil, fmt.Errorf("cross-namespace ExternalProvider reference %q is not supported", ref.Ref.Namespace+"/"+ref.Ref.Name)
	}
	providerKey := types.NamespacedName{Namespace: namespace, Name: ref.Ref.Name}
	providerInfo, found := r.store.getProvider(providerKey)
	if !found {
		return nil, fmt.Errorf("ExternalProvider %q not yet available in store", ref.Ref.Name)
	}

	config := mergeConfig(providerInfo.config, ref.Config)

	secretName := providerInfo.secretName
	secretNamespace := providerInfo.secretNamespace
	authType := providerInfo.auth
	if ref.Auth != nil {
		secretName = ref.Auth.SecretRef.Name
		secretNamespace = namespace
		authType = auth.Auth(ref.Auth.Type)
	}

	weight := 1
	if ref.Weight != nil {
		weight = *ref.Weight
	}

	path, err := ctrlcommon.ResolvePath(ref.Path, config, ref.TargetModel)
	if err != nil {
		return nil, fmt.Errorf("path %q: %w", ref.Path, err)
	}

	return &resolvedProviderRef{
		provider:        providerInfo.provider,
		providerName:    ref.Ref.Name,
		targetModel:     ref.TargetModel,
		apiFormat:       apiformat.APIFormat(ref.APIFormat),
		auth:            authType,
		endpoint:        providerInfo.endpoint,
		path:            path,
		secretName:      secretName,
		secretNamespace: secretNamespace,
		config:          config,
		weight:          weight,
	}, nil
}

// mergeConfig copies provider config and applies model overrides.
func mergeConfig(providerConfig, modelConfig map[string]string) map[string]string {
	merged := make(map[string]string, len(providerConfig))
	for k, v := range providerConfig {
		merged[k] = v
	}
	for k, v := range modelConfig {
		merged[k] = v
	}
	return merged
}
