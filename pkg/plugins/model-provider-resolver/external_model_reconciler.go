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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	logutil "sigs.k8s.io/gateway-api-inference-extension/pkg/common/observability/logging"
)

var externalModelGVK = schema.GroupVersionKind{
	Group:   "inference.opendatahub.io",
	Version: "v1alpha1",
	Kind:    "ExternalModel",
}

type externalModelReconciler struct {
	client.Reader
	modelStore    *modelInfoStore
	providerStore *providerInfoStore
}

func (r *externalModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	logger.Info("reconciling ExternalModel", "name", req.Name, "namespace", req.Namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(externalModelGVK)

	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get ExternalModel: %w", err)
	}

	if errors.IsNotFound(err) || !obj.GetDeletionTimestamp().IsZero() {
		r.modelStore.deleteExternalModel(req.NamespacedName)
		logger.Info("ExternalModel removed from store", "name", req.Name, "namespace", req.Namespace)
		return ctrl.Result{}, nil
	}

	refs, _, _ := unstructured.NestedSlice(obj.Object, "spec", "externalProviderRefs")
	// CRD validation ensures at least one ref, but guard defensively
	// TODO: extend to support multiple provider refs (#278)
	refMap, _ := refs[0].(map[string]any)

	providerRefName := nestedString(refMap, "ref", "name")
	targetModel := nestedString(refMap, "targetModel")

	providerKey := types.NamespacedName{Namespace: req.Namespace, Name: providerRefName}
	providerInfo, found := r.providerStore.get(providerKey)
	if !found {
		logger.Info("ExternalProvider not yet available, requeuing", "provider", providerRefName)
		return ctrl.Result{Requeue: true}, nil
	}

	info := &externalModelInfo{
		provider:        providerInfo.provider,
		targetModel:     targetModel,
		secretName:      providerInfo.secretName,
		secretNamespace: providerInfo.secretNamespace,
		config:          providerInfo.config,
	}
	r.modelStore.addOrUpdateExternalModel(req.NamespacedName, info)

	logger.Info("updated model store", "provider", providerInfo.provider, "targetModel", targetModel)
	return ctrl.Result{}, nil
}

func nestedString(obj map[string]any, fields ...string) string {
	current := obj
	for i, f := range fields {
		if i == len(fields)-1 {
			s, _ := current[f].(string)
			return s
		}
		next, ok := current[f].(map[string]any)
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}
