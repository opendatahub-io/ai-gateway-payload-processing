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

package apikey_injection

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
)

const (
	// managedLabel selects Secrets managed by the apikey-injection plugin.
	// The Secret informer uses this as a list/watch label selector; Reconcile
	// also requires this label before caching Secret data.
	managedLabel = "inference.llm-d.ai/ipp-managed"
)

func hasManagedLabel(object client.Object) bool {
	return object.GetLabels()[managedLabel] == "true"
}

// secretReconciler watches Secrets and updates the secretStore.
type secretReconciler struct {
	client.Reader
	store *secretStore
}

// Reconcile handles create/update/delete events for Secrets.
func (r *secretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	key := req.String()
	logger.Info("reconciling Secret", "key", key)

	secret := &corev1.Secret{}
	err := r.Get(ctx, req.NamespacedName, secret)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get Secret: %w", err)
	}

	if errors.IsNotFound(err) || !secret.DeletionTimestamp.IsZero() || !hasManagedLabel(secret) {
		r.store.delete(req.Namespace, req.Name)
		logger.Info("Secret removed from store", "key", key)
		return ctrl.Result{}, nil
	}

	if err := r.store.addOrUpdate(req.Namespace, req.Name, secret); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to add or update Secret %s: %w", key, err)
	}

	logger.Info("Secret added/updated in store", "key", key)
	return ctrl.Result{}, nil
}
