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
)

// maasModelRefGVK is the GroupVersionKind for MaaSModelRef CRD.
var maasModelRefGVK = schema.GroupVersionKind{
	Group:   "maas.opendatahub.io",
	Version: "v1alpha1",
	Kind:    "MaaSModelRef",
}

// maasModelRefReconciler watches MaaSModelRef CRDs (via unstructured client)
// and updates the model store with the mapping between MaaSModelRef and ExternalModel.
type maasModelRefReconciler struct {
	client.Reader
	store *modelInfoStore
}

// Reconcile handles create/update/delete events for MaaSModelRef resources.
func (r *maasModelRefReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling MaaSModelRef", "name", req.Name, "namespace", req.Namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(maasModelRefGVK)

	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get MaaSModelRef: %w", err)
	}

	if !r.isExternalModel(obj) { // don't store information about models other than ExternalModel
		return ctrl.Result{}, nil
	}
	// if we're here, the used "kind" in spec.modelRef is ExternalModel

	if errors.IsNotFound(err) || !obj.GetDeletionTimestamp().IsZero() {
		r.store.deleteMaaSModelRef(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	externalModelName, _, _ := unstructured.NestedString(obj.Object, "spec", "modelRef", "name")
	// ExternalModel is always in the same namespace as MaaSModelRef
	r.store.addOrUpdateMaaSModelRef(req.NamespacedName, types.NamespacedName{Namespace: req.Namespace, Name: externalModelName})

	logger.Info("Updated model store")
	return ctrl.Result{}, nil
}

func (r *maasModelRefReconciler) isExternalModel(object *unstructured.Unstructured) bool {
	kind, _, _ := unstructured.NestedString(object.Object, "spec", "modelRef", "kind")
	if kind == "ExternalModel" {
		return true
	}

	return false
}
