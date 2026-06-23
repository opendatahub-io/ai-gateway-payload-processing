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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
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

	// Resolve all refs whose providers are available in the store.
	var resolved []*resolvedProviderRef
	for i := range model.Spec.ExternalProviderRefs {
		resolvedRef, found := r.resolveRef(req.Namespace, &model.Spec.ExternalProviderRefs[i])
		if !found {
			logger.Info("ExternalProvider not yet available, skipping ref", "provider", model.Spec.ExternalProviderRefs[i].Ref.Name)
			continue
		}
		resolved = append(resolved, resolvedRef)
	}

	if len(resolved) == 0 {
		logger.Info("no ExternalProvider available for any ref, requeuing")
		return ctrl.Result{RequeueAfter: providerRequeueDelay}, nil
	}

	modelName := model.Spec.ModelName
	if modelName == "" {
		modelName = req.Name
	}

	r.store.addOrUpdateModel(req.NamespacedName, &externalModelInfo{modelName: modelName, refs: resolved})
	logger.Info("updated model store", "modelName", modelName, "resolvedRefs", len(resolved))
	return ctrl.Result{}, nil
}

// resolveRef resolves a single ExternalProviderRef to provider info.
// Returns (nil, false) if the provider is not yet available in the store.
func (r *externalModelReconciler) resolveRef(namespace string, ref *inferencev1alpha1.ExternalProviderRef) (*resolvedProviderRef, bool) {
	providerKey := types.NamespacedName{Namespace: namespace, Name: ref.Ref.Name}
	providerInfo, found := r.store.getProvider(providerKey)
	if !found {
		return nil, false
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

	path := providerInfo.path
	if ref.Path != "" {
		path = ref.Path
	}

	return &resolvedProviderRef{
		provider:        providerInfo.provider,
		targetModel:     ref.TargetModel,
		apiFormat:       apiformat.APIFormat(ref.APIFormat),
		auth:            authType,
		endpoint:        providerInfo.endpoint,
		path:            path,
		secretName:      secretName,
		secretNamespace: secretNamespace,
		config:          config,
		weight:          weight,
	}, true
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
