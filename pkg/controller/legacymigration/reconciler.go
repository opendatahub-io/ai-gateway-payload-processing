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

package legacymigration

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
)

const (
	labelManagedBy    = "app.kubernetes.io/managed-by"
	labelMigratedFrom = "inference.opendatahub.io/migrated-from"
	managedByValue    = "ipp-legacy-migration"
)

var LegacyExternalModelGVK = schema.GroupVersionKind{
	Group:   "maas.opendatahub.io",
	Version: "v1alpha1",
	Kind:    "ExternalModel",
}

//+kubebuilder:rbac:groups=maas.opendatahub.io,resources=externalmodels,verbs=get;list;watch
//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalproviders,verbs=get;list;watch;create;update
//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalmodels,verbs=get;list;watch;create;update

// Reconciler watches legacy maas.opendatahub.io ExternalModel CRs and creates
// corresponding inference.opendatahub.io ExternalProvider + ExternalModel CRs.
// The new CRs trigger the existing controller flow (HTTPRoute, Service, etc.).
type Reconciler struct {
	client.Client
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("reconciling legacy ExternalModel", "name", req.Name, "namespace", req.Namespace)

	old := &unstructured.Unstructured{}
	old.SetGroupVersionKind(LegacyExternalModelGVK)
	if err := r.Get(ctx, req.NamespacedName, old); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !old.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	providerName, _, _ := unstructured.NestedString(old.Object, "spec", "provider")
	endpoint, _, _ := unstructured.NestedString(old.Object, "spec", "endpoint")
	targetModel, _, _ := unstructured.NestedString(old.Object, "spec", "targetModel")
	credsName, _, _ := unstructured.NestedString(old.Object, "spec", "credentialRef", "name")

	if providerName == "" || endpoint == "" || targetModel == "" {
		logger.Info("legacy ExternalModel missing required fields, skipping",
			"provider", providerName, "endpoint", endpoint, "targetModel", targetModel)
		return ctrl.Result{}, nil
	}

	labels := map[string]string{
		labelManagedBy:    managedByValue,
		labelMigratedFrom: req.Name,
	}

	desiredProvider := &inferencev1alpha1.ExternalProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: inferencev1alpha1.ExternalProviderSpec{
			Provider: providerName,
			Endpoint: endpoint,
			Auth: inferencev1alpha1.AuthConfig{
				Type:      "apikey",
				SecretRef: inferencev1alpha1.NameReference{Name: credsName},
			},
		},
	}

	desiredModel := &inferencev1alpha1.ExternalModel{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: inferencev1alpha1.ExternalModelSpec{
			ExternalProviderRefs: []inferencev1alpha1.ExternalProviderRef{
				{
					Ref:         inferencev1alpha1.NameReference{Name: req.Name},
					TargetModel: targetModel,
					APIFormat:   mapProviderToAPIFormat(providerName),
					Path:        mapProviderToDefaultPath(providerName),
				},
			},
		},
	}

	ownerRef := ownerReferenceFromLegacy(old)
	desiredProvider.OwnerReferences = []metav1.OwnerReference{ownerRef}
	desiredModel.OwnerReferences = []metav1.OwnerReference{ownerRef}

	if err := r.applyProvider(ctx, desiredProvider); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply ExternalProvider: %w", err)
	}

	if err := r.applyModel(ctx, desiredModel); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply ExternalModel: %w", err)
	}

	logger.Info("legacy migration complete",
		"provider", providerName, "model", req.Name, "targetModel", targetModel)
	return ctrl.Result{}, nil
}

func (r *Reconciler) applyProvider(ctx context.Context, desired *inferencev1alpha1.ExternalProvider) error {
	existing := &inferencev1alpha1.ExternalProvider{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		log.FromContext(ctx).Info("creating ExternalProvider", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if !isManagedByMigration(existing.Labels) {
		log.FromContext(ctx).Info("skipping ExternalProvider not managed by migration", "name", desired.Name)
		return nil
	}
	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		return nil
	}
	existing.Spec = desired.Spec
	log.FromContext(ctx).Info("updating ExternalProvider", "name", desired.Name)
	return r.Update(ctx, existing)
}

func (r *Reconciler) applyModel(ctx context.Context, desired *inferencev1alpha1.ExternalModel) error {
	existing := &inferencev1alpha1.ExternalModel{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		log.FromContext(ctx).Info("creating ExternalModel", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if !isManagedByMigration(existing.Labels) {
		log.FromContext(ctx).Info("skipping ExternalModel not managed by migration", "name", desired.Name)
		return nil
	}
	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) {
		return nil
	}
	existing.Spec = desired.Spec
	log.FromContext(ctx).Info("updating ExternalModel", "name", desired.Name)
	return r.Update(ctx, existing)
}

func ownerReferenceFromLegacy(old *unstructured.Unstructured) metav1.OwnerReference {
	isController := true
	blockDeletion := true
	return metav1.OwnerReference{
		APIVersion:         old.GetAPIVersion(),
		Kind:               old.GetKind(),
		Name:               old.GetName(),
		UID:                old.GetUID(),
		Controller:         &isController,
		BlockOwnerDeletion: &blockDeletion,
	}
}

func isManagedByMigration(labels map[string]string) bool {
	return labels != nil && labels[labelManagedBy] == managedByValue
}

func mapProviderToAPIFormat(provider string) string {
	if provider == "anthropic" {
		return "messages"
	}
	return "openai-chat"
}

func mapProviderToDefaultPath(provider string) string {
	switch provider {
	case "anthropic":
		return "/v1/messages"
	case "azure", "azure-openai":
		return "/openai/v1/chat/completions"
	case "vertex-openai":
		return "/v1/projects/{project}/locations/{location}/endpoints/{endpoint}/chat/completions"
	default:
		return "/v1/chat/completions"
	}
}
