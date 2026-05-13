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

var externalProviderGVK = schema.GroupVersionKind{
	Group:   "inference.opendatahub.io",
	Version: "v1alpha1",
	Kind:    "ExternalProvider",
}

type externalProviderReconciler struct {
	client.Reader
	store *providerInfoStore
}

func (r *externalProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ExternalProvider", "name", req.Name, "namespace", req.Namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(externalProviderGVK)

	err := r.Get(ctx, req.NamespacedName, obj)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get ExternalProvider: %w", err)
	}

	if errors.IsNotFound(err) || !obj.GetDeletionTimestamp().IsZero() {
		r.store.delete(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	providerType, _, _ := unstructured.NestedString(obj.Object, "spec", "provider")
	endpoint, _, _ := unstructured.NestedString(obj.Object, "spec", "endpoint")
	secretName, _, _ := unstructured.NestedString(obj.Object, "spec", "auth", "secretRef", "name")

	if providerType == "" || endpoint == "" || secretName == "" {
		r.store.delete(req.NamespacedName)
		logger.Info("ExternalProvider missing required fields, removing from store",
			"provider", providerType, "endpoint", endpoint, "secretName", secretName)
		return ctrl.Result{}, nil
	}

	configRaw, _, _ := unstructured.NestedStringMap(obj.Object, "spec", "config")

	r.store.addOrUpdate(req.NamespacedName, &providerInfo{
		provider:        providerType,
		endpoint:        endpoint,
		secretName:      secretName,
		secretNamespace: req.Namespace,
		config:          configRaw,
	})

	logger.Info("Updated provider store", "provider", providerType, "endpoint", endpoint)
	return ctrl.Result{}, nil
}
