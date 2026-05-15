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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
)

// legacyExternalModelGVK is the old MaaS ExternalModel CRD (maas.opendatahub.io).
// Kept for backward compatibility with existing deployments.
var legacyExternalModelGVK = schema.GroupVersionKind{
	Group:   "maas.opendatahub.io",
	Version: "v1alpha1",
	Kind:    "ExternalModel",
}

// externalModelReconciler watches inference.opendatahub.io ExternalModel CRDs
// using typed clients and resolves provider info from the provider store.
type externalModelReconciler struct {
	client.Reader
	modelStore    *modelInfoStore
	providerStore *providerInfoStore
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
		r.modelStore.deleteExternalModel(req.NamespacedName)
		logger.Info("ExternalModel removed from store", "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, nil
	}

	// CRD validation ensures at least one ref (MinItems=1)
	// TODO: extend to support multiple provider refs (#278)
	ref := model.Spec.ExternalProviderRefs[0]

	providerKey := types.NamespacedName{Namespace: req.Namespace, Name: ref.Ref.Name}
	providerInfo, found := r.providerStore.get(providerKey)
	if !found {
		logger.Info("ExternalProvider not yet available, requeuing", "provider", ref.Ref.Name)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	info := &externalModelInfo{
		provider:        providerInfo.provider,
		targetModel:     ref.TargetModel,
		secretName:      providerInfo.secretName,
		secretNamespace: providerInfo.secretNamespace,
		config:          providerInfo.config,
	}
	r.modelStore.addOrUpdateExternalModel(req.NamespacedName, info)

	logger.Info("updated model store", "provider", providerInfo.provider, "targetModel", ref.TargetModel)
	return ctrl.Result{}, nil
}

// legacyExternalModelReconciler handles the flat maas.opendatahub.io ExternalModel
// CRD structure (spec.provider, spec.targetModel, spec.credentialRef).
// Uses unstructured client because the types are in a different repo.
type legacyExternalModelReconciler struct {
	client.Reader
	store *modelInfoStore
}

func (r *legacyExternalModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	logger.Info("reconciling legacy ExternalModel", "name", req.Name, "namespace", req.Namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(legacyExternalModelGVK)

	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get ExternalModel: %w", err)
	}

	if errors.IsNotFound(err) || !obj.GetDeletionTimestamp().IsZero() {
		r.store.deleteExternalModel(req.NamespacedName)
		logger.Info("ExternalModel removed from store", "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, nil
	}

	provider, _, _ := unstructured.NestedString(obj.Object, "spec", "provider")
	targetModel, _, _ := unstructured.NestedString(obj.Object, "spec", "targetModel")
	credsName, _, _ := unstructured.NestedString(obj.Object, "spec", "credentialRef", "name")

	info := &externalModelInfo{
		provider:        provider,
		targetModel:     targetModel,
		secretName:      credsName,
		secretNamespace: req.Namespace,
	}
	r.store.addOrUpdateExternalModel(req.NamespacedName, info)

	logger.Info("updated model store", "provider", provider, "targetModel", targetModel)
	return ctrl.Result{}, nil
}
