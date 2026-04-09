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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// externalModelGVK is the GroupVersionKind for ExternalModel CRD.
var externalModelGVK = schema.GroupVersionKind{
	Group:   "maas.opendatahub.io",
	Version: "v1alpha1",
	Kind:    "ExternalModel",
}

// externalModelReconciler watches ExternalModel CRDs (via unstructured client)
// and updates the model store with provider and credential information.
type externalModelReconciler struct {
	client.Reader
	store *modelInfoStore
}

// Reconcile handles create/update/delete events for ExternalModel resources.
// The ExternalModel CR name is used as the model key in the store, matching
// the model name in inference request bodies.
func (r *externalModelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ExternalModel", "name", req.Name, "namespace", req.Namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(externalModelGVK)

	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get ExternalModel: %w", err)
	}

	if errors.IsNotFound(err) || !obj.GetDeletionTimestamp().IsZero() {
		r.store.deleteExternalModel(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	provider, _, _ := unstructured.NestedString(obj.Object, "spec", "provider")
	targetModel, _, _ := unstructured.NestedString(obj.Object, "spec", "targetModel")
	credsName, _, _ := unstructured.NestedString(obj.Object, "spec", "credentialRef", "name")

	// targetModel is the model that will be used in the request body when getting inference requests.
	info := &externalModelInfo{
		provider:        provider,
		targetModel:     targetModel,
		secretName:      credsName,
		secretNamespace: req.Namespace, // secret namespace is always the namespace of the ExternalModel
	}
	r.store.addOrUpdateExternalModel(req.NamespacedName, info)

	logger.Info("Updated model store", "provider", provider, "targetModel", targetModel)
	return ctrl.Result{}, nil
}
