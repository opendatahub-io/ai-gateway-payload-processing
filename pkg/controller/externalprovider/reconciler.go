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

package externalprovider

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	ctrlcommon "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/common"
)

const (
	labelExternalProvider = "inference.opendatahub.io/external-provider"
	managedByValue        = "ipp-external-provider-reconciler"
)

//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalproviders,verbs=get;list;watch
//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalproviders/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalproviders/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;delete
//+kubebuilder:rbac:groups=networking.istio.io,resources=serviceentries,verbs=get;list;watch;create;update;delete
//+kubebuilder:rbac:groups=networking.istio.io,resources=destinationrules,verbs=get;list;watch;create;update;delete

// Reconciler watches ExternalProvider CRs and creates the shared Istio
// networking resources (Service, ServiceEntry, DestinationRule) for each
// external provider endpoint.
type Reconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ExternalProvider")

	provider := &inferencev1alpha1.ExternalProvider{}
	if err := r.Get(ctx, req.NamespacedName, provider); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !provider.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	if provider.Status.Phase == "" {
		r.setStatus(ctx, logger, provider, "Pending", metav1.ConditionFalse, "Reconciling", "Reconciliation in progress")
	}

	if err := r.validateSecretRef(ctx, provider); err != nil {
		r.setStatus(ctx, logger, provider, "Failed", metav1.ConditionFalse, "SecretNotFound", err.Error())
		return ctrl.Result{}, err
	}

	if err := r.reconcileResources(ctx, logger, provider); err != nil {
		r.setStatus(ctx, logger, provider, "Failed", metav1.ConditionFalse, "ReconcileFailed", err.Error())
		return ctrl.Result{}, err
	}

	r.setStatus(ctx, logger, provider, "Ready", metav1.ConditionTrue, "Reconciled", "All resources created successfully")
	return ctrl.Result{}, nil
}

func (r *Reconciler) reconcileResources(ctx context.Context, logger logr.Logger, provider *inferencev1alpha1.ExternalProvider) error {
	name := provider.Name
	ns := provider.Namespace
	endpoint := provider.Spec.Endpoint
	labels := commonLabels(name)
	port := ctrlcommon.DefaultTLSPort

	// 1. ExternalName Service
	svc := buildService(endpoint, name, ns, port, labels)
	if err := controllerutil.SetControllerReference(provider, svc, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner on Service: %w", err)
	}
	if err := r.applyService(ctx, logger, svc); err != nil {
		return fmt.Errorf("failed to apply Service: %w", err)
	}

	// 2. ServiceEntry
	se := buildServiceEntry(endpoint, name, ns, port, labels)
	setUnstructuredOwner(provider, se)
	if err := r.applyUnstructured(ctx, logger, se); err != nil {
		return fmt.Errorf("failed to apply ServiceEntry: %w", err)
	}

	// 3. DestinationRule (TLS origination)
	dr := buildDestinationRule(endpoint, name, ns, labels)
	setUnstructuredOwner(provider, dr)
	if err := r.applyUnstructured(ctx, logger, dr); err != nil {
		return fmt.Errorf("failed to apply DestinationRule: %w", err)
	}

	logger.Info("ExternalProvider resources reconciled",
		"service", name,
		"serviceEntry", name,
		"destinationRule", name,
	)
	return nil
}

func (r *Reconciler) validateSecretRef(ctx context.Context, provider *inferencev1alpha1.ExternalProvider) error {
	key := types.NamespacedName{
		Name:      provider.Spec.Auth.SecretRef.Name,
		Namespace: provider.Namespace,
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, key, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("secret %q not found in namespace %q", key.Name, key.Namespace)
		}
		return fmt.Errorf("failed to get secret %q: %w", key.Name, err)
	}
	return nil
}

func (r *Reconciler) setStatus(ctx context.Context, logger logr.Logger, provider *inferencev1alpha1.ExternalProvider, phase string, condStatus metav1.ConditionStatus, reason, message string) {
	provider.Status.Phase = phase
	meta.SetStatusCondition(&provider.Status.Conditions, metav1.Condition{
		Type:               ctrlcommon.ConditionTypeReady,
		Status:             condStatus,
		ObservedGeneration: provider.Generation,
		Reason:             reason,
		Message:            message,
	})
	if err := r.Status().Update(ctx, provider); err != nil {
		logger.Error(err, "failed to update ExternalProvider status")
	}
}

func setUnstructuredOwner(owner *inferencev1alpha1.ExternalProvider, obj *unstructured.Unstructured) {
	isController := true
	blockDeletion := true
	obj.SetOwnerReferences([]metav1.OwnerReference{
		{
			APIVersion:         inferencev1alpha1.GroupVersion.String(),
			Kind:               "ExternalProvider",
			Name:               owner.Name,
			UID:                owner.UID,
			Controller:         &isController,
			BlockOwnerDeletion: &blockDeletion,
		},
	})
}

func (r *Reconciler) applyService(ctx context.Context, logger logr.Logger, desired *corev1.Service) error {
	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating Service", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if !equality.Semantic.DeepEqual(existing.Spec, desired.Spec) ||
		!equality.Semantic.DeepEqual(existing.Labels, desired.Labels) ||
		!equality.Semantic.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) {
		existing.Spec = desired.Spec
		existing.Labels = desired.Labels
		existing.OwnerReferences = desired.OwnerReferences
		logger.Info("Updating Service", "name", desired.Name)
		return r.Update(ctx, existing)
	}
	return nil
}

func (r *Reconciler) applyUnstructured(ctx context.Context, logger logr.Logger, desired *unstructured.Unstructured) error {
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(desired.GroupVersionKind())
	err := r.Get(ctx, types.NamespacedName{Name: desired.GetName(), Namespace: desired.GetNamespace()}, existing)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating resource", "kind", desired.GetKind(), "name", desired.GetName())
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(existing.Object["spec"], desired.Object["spec"]) &&
		equality.Semantic.DeepEqual(existing.GetLabels(), desired.GetLabels()) &&
		equality.Semantic.DeepEqual(existing.GetOwnerReferences(), desired.GetOwnerReferences()) {
		return nil
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	logger.Info("Updating resource", "kind", desired.GetKind(), "name", desired.GetName())
	return r.Update(ctx, desired)
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	managedByPredicate, err := predicate.LabelSelectorPredicate(metav1.LabelSelector{
		MatchLabels: map[string]string{ctrlcommon.LabelManagedBy: managedByValue},
	})
	if err != nil {
		return err
	}

	seObj := &unstructured.Unstructured{}
	seObj.SetGroupVersionKind(schema.GroupVersionKind{Group: "networking.istio.io", Version: "v1", Kind: "ServiceEntry"})

	drObj := &unstructured.Unstructured{}
	drObj.SetGroupVersionKind(schema.GroupVersionKind{Group: "networking.istio.io", Version: "v1", Kind: "DestinationRule"})

	ownerHandler := handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &inferencev1alpha1.ExternalProvider{})

	return ctrl.NewControllerManagedBy(mgr).
		For(&inferencev1alpha1.ExternalProvider{}).
		Owns(&corev1.Service{}, builder.WithPredicates(managedByPredicate)).
		Watches(seObj, ownerHandler, builder.WithPredicates(managedByPredicate)).
		Watches(drObj, ownerHandler, builder.WithPredicates(managedByPredicate)).
		Named("external-provider-reconciler").
		Complete(r)
}

func commonLabels(providerName string) map[string]string {
	return map[string]string{
		ctrlcommon.LabelManagedBy: managedByValue,
		labelExternalProvider:     providerName,
	}
}

func buildService(endpoint, name, namespace string, port int32, labels map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: endpoint,
			Ports: []corev1.ServicePort{
				{
					Port:       port,
					TargetPort: intstr.FromInt32(port),
				},
			},
		},
	}
}

func buildServiceEntry(endpoint, name, namespace string, port int32, labels map[string]string) *unstructured.Unstructured {
	se := &unstructured.Unstructured{}
	se.SetAPIVersion("networking.istio.io/v1")
	se.SetKind("ServiceEntry")
	se.SetName(name)
	se.SetNamespace(namespace)
	se.SetLabels(labels)

	se.Object["spec"] = map[string]any{
		"hosts":      []any{endpoint},
		"location":   "MESH_EXTERNAL",
		"resolution": "DNS",
		"ports": []any{
			map[string]any{
				"number":   int64(port),
				"name":     "https",
				"protocol": "HTTPS",
			},
		},
	}
	return se
}

func buildDestinationRule(endpoint, name, namespace string, labels map[string]string) *unstructured.Unstructured {
	dr := &unstructured.Unstructured{}
	dr.SetAPIVersion("networking.istio.io/v1")
	dr.SetKind("DestinationRule")
	dr.SetName(name)
	dr.SetNamespace(namespace)
	dr.SetLabels(labels)

	dr.Object["spec"] = map[string]any{
		"host": endpoint,
		"trafficPolicy": map[string]any{
			"tls": map[string]any{
				"mode": "SIMPLE",
			},
		},
	}
	return dr
}
