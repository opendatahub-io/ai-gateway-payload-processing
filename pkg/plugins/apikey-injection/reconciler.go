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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// managedLabel selects Secrets managed by the apikey-injection plugin.
	// Only Secrets carrying this label are watched by the reconciler.
	managedLabel = "inference.networking.k8s.io/bbr-managed"
)

func hasManagedLabel(object client.Object) bool {
	return object.GetLabels()[managedLabel] == "true"
}

// managedLabelPredicate filters events to only Secrets labeled with
// "inference.networking.k8s.io/bbr-managed" = "true".
// For updates, it accepts the event when either the old or new object carries
// the label so that label-removal is visible to the reconciler.
func managedLabelPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return hasManagedLabel(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return hasManagedLabel(e.ObjectOld) || hasManagedLabel(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return hasManagedLabel(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return hasManagedLabel(e.Object) },
	}
}

// secretReconciler watches Secrets and updates the secretStore.
type secretReconciler struct {
	client.Reader
	store *secretStore
}

// Reconcile handles create/update/delete events for Secrets.
func (r *secretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	key := req.String()
	logger.Info("Reconciling Secret", "key", key)

	secret := &corev1.Secret{}
	err := r.Get(ctx, req.NamespacedName, secret)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("unable to get Secret: %w", err)
	}

	if errors.IsNotFound(err) || !secret.DeletionTimestamp.IsZero() || !hasManagedLabel(secret) {
		r.store.delete(key)
		return ctrl.Result{}, nil
	}

	if err := r.store.addOrUpdate(key, secret); err != nil {
		return ctrl.Result{}, fmt.Errorf("unable to add or update Secret %s: %w", key, err)
	}

	return ctrl.Result{}, nil
}
