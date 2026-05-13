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

package externalmodel

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gatewayapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	inferencev1alpha1 "github.com/opendatahub-io/ai-gateway-payload-processing/api/inference/v1alpha1"
	ctrlcommon "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/common"
)

const (
	labelExternalModel = "inference.opendatahub.io/external-model"
	managedByValue     = "ipp-external-model-reconciler"
)

//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalmodels,verbs=get;list;watch
//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalmodels/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalmodels/finalizers,verbs=update
//+kubebuilder:rbac:groups=inference.opendatahub.io,resources=externalproviders,verbs=get;list;watch
//+kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=httproutes,verbs=get;list;watch;create;update;delete

// Reconciler watches ExternalModel CRs, resolves the referenced ExternalProvider,
// and creates an HTTPRoute that routes client traffic to the provider's Service.
type Reconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	GatewayName      string
	GatewayNamespace string
	RouteTimeout     string
}

func (r *Reconciler) gatewayName() string {
	if r.GatewayName != "" {
		return r.GatewayName
	}
	return ctrlcommon.DefaultGatewayName
}

func (r *Reconciler) gatewayNamespace() string {
	if r.GatewayNamespace != "" {
		return r.GatewayNamespace
	}
	return ctrlcommon.DefaultGatewayNamespace
}

func (r *Reconciler) routeTimeout() string {
	if r.RouteTimeout != "" {
		return r.RouteTimeout
	}
	return ctrlcommon.DefaultRouteTimeout
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ExternalModel")

	model := &inferencev1alpha1.ExternalModel{}
	if err := r.Get(ctx, req.NamespacedName, model); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !model.GetDeletionTimestamp().IsZero() {
		return ctrl.Result{}, nil
	}

	if model.Status.Phase == "" {
		r.setStatus(ctx, logger, model, "Pending", metav1.ConditionFalse, "Reconciling", "Reconciliation in progress")
	}

	if err := r.reconcileHTTPRoute(ctx, logger, model); err != nil {
		r.setStatus(ctx, logger, model, "Failed", metav1.ConditionFalse, "ReconcileFailed", err.Error())
		return ctrl.Result{}, err
	}

	r.setStatus(ctx, logger, model, "Ready", metav1.ConditionTrue, "Reconciled", "HTTPRoute created successfully")
	return ctrl.Result{}, nil
}

func (r *Reconciler) reconcileHTTPRoute(ctx context.Context, logger logr.Logger, model *inferencev1alpha1.ExternalModel) error {
	if len(model.Spec.ExternalProviderRefs) == 0 {
		return fmt.Errorf("ExternalModel %q has no externalProviderRefs", model.Name)
	}
	ref := model.Spec.ExternalProviderRefs[0]

	provider := &inferencev1alpha1.ExternalProvider{}
	providerKey := types.NamespacedName{Name: ref.Ref.Name, Namespace: model.Namespace}
	if err := r.Get(ctx, providerKey, provider); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("ExternalProvider %q not found in namespace %q", ref.Ref.Name, model.Namespace)
		}
		return fmt.Errorf("failed to get ExternalProvider %q: %w", ref.Ref.Name, err)
	}

	if provider.Status.Phase != "Ready" {
		return fmt.Errorf("ExternalProvider %q is not ready (phase: %s)", ref.Ref.Name, provider.Status.Phase)
	}

	labels := commonLabels(model.Name)
	hr := buildHTTPRoute(
		provider.Spec.Endpoint,
		provider.Name,
		model.Name,
		ref.TargetModel,
		model.Namespace,
		ctrlcommon.DefaultTLSPort,
		r.gatewayName(),
		r.gatewayNamespace(),
		r.routeTimeout(),
		labels,
	)

	if err := controllerutil.SetControllerReference(model, hr, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner on HTTPRoute: %w", err)
	}

	if err := r.applyHTTPRoute(ctx, logger, hr); err != nil {
		return fmt.Errorf("failed to apply HTTPRoute: %w", err)
	}

	logger.Info("ExternalModel HTTPRoute reconciled",
		"httpRoute", model.Name,
		"provider", provider.Name,
		"targetModel", ref.TargetModel,
	)
	return nil
}

func (r *Reconciler) setStatus(ctx context.Context, logger logr.Logger, model *inferencev1alpha1.ExternalModel, phase string, condStatus metav1.ConditionStatus, reason, message string) {
	model.Status.Phase = phase
	meta.SetStatusCondition(&model.Status.Conditions, metav1.Condition{
		Type:               ctrlcommon.ConditionTypeReady,
		Status:             condStatus,
		ObservedGeneration: model.Generation,
		Reason:             reason,
		Message:            message,
	})
	if err := r.Status().Update(ctx, model); err != nil {
		logger.Error(err, "failed to update ExternalModel status")
	}
}

func (r *Reconciler) applyHTTPRoute(ctx context.Context, logger logr.Logger, desired *gatewayapiv1.HTTPRoute) error {
	existing := &gatewayapiv1.HTTPRoute{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		logger.Info("Creating HTTPRoute", "name", desired.Name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(existing.Spec, desired.Spec) &&
		equality.Semantic.DeepEqual(existing.Labels, desired.Labels) &&
		equality.Semantic.DeepEqual(existing.OwnerReferences, desired.OwnerReferences) {
		return nil
	}
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	existing.OwnerReferences = desired.OwnerReferences
	logger.Info("Updating HTTPRoute", "name", desired.Name)
	return r.Update(ctx, existing)
}

// MapProviderToModels returns reconcile requests for all ExternalModels that
// reference the changed ExternalProvider.
func (r *Reconciler) MapProviderToModels(ctx context.Context, obj client.Object) []reconcile.Request {
	provider := obj.(*inferencev1alpha1.ExternalProvider)
	modelList := &inferencev1alpha1.ExternalModelList{}
	if err := r.List(ctx, modelList, client.InNamespace(provider.Namespace)); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for i := range modelList.Items {
		model := &modelList.Items[i]
		for _, ref := range model.Spec.ExternalProviderRefs {
			if ref.Ref.Name == provider.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      model.Name,
						Namespace: model.Namespace,
					},
				})
			}
		}
	}
	return requests
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&inferencev1alpha1.ExternalModel{}).
		Owns(&gatewayapiv1.HTTPRoute{}).
		Watches(&inferencev1alpha1.ExternalProvider{},
			handler.EnqueueRequestsFromMapFunc(r.MapProviderToModels)).
		Named("external-model-reconciler").
		Complete(r)
}

func commonLabels(modelName string) map[string]string {
	return map[string]string{
		ctrlcommon.LabelManagedBy: managedByValue,
		labelExternalModel:        modelName,
	}
}

func buildHTTPRoute(providerEndpoint, providerName, modelName, targetModel, namespace string, port int32, gatewayName, gatewayNamespace, routeTimeout string, labels map[string]string) *gatewayapiv1.HTTPRoute {
	gwNamespace := gatewayapiv1.Namespace(gatewayNamespace)
	pathType := gatewayapiv1.PathMatchPathPrefix
	pathPrefix := "/" + namespace + "/" + modelName
	headerType := gatewayapiv1.HeaderMatchExact
	gwPort := gatewayapiv1.PortNumber(port)
	timeout := gatewayapiv1.Duration(routeTimeout)

	backendRefs := []gatewayapiv1.HTTPBackendRef{
		{
			BackendRef: gatewayapiv1.BackendRef{
				BackendObjectReference: gatewayapiv1.BackendObjectReference{
					Name: gatewayapiv1.ObjectName(providerName),
					Port: &gwPort,
				},
			},
		},
	}

	filters := []gatewayapiv1.HTTPRouteFilter{
		{
			Type: gatewayapiv1.HTTPRouteFilterRequestHeaderModifier,
			RequestHeaderModifier: &gatewayapiv1.HTTPHeaderFilter{
				Set: []gatewayapiv1.HTTPHeader{
					{
						Name:  "Host",
						Value: providerEndpoint,
					},
				},
			},
		},
	}

	return &gatewayapiv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      modelName,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: gatewayapiv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayapiv1.CommonRouteSpec{
				ParentRefs: []gatewayapiv1.ParentReference{
					{
						Name:      gatewayapiv1.ObjectName(gatewayName),
						Namespace: &gwNamespace,
					},
				},
			},
			Rules: []gatewayapiv1.HTTPRouteRule{
				// TODO: remove path prefix rule when unified entrypoint (RHAISTRAT-1540) is wired.
				{
					Matches: []gatewayapiv1.HTTPRouteMatch{
						{
							Path: &gatewayapiv1.HTTPPathMatch{
								Type:  &pathType,
								Value: &pathPrefix,
							},
						},
					},
					BackendRefs: backendRefs,
					Filters:     filters,
					Timeouts:    &gatewayapiv1.HTTPRouteTimeouts{Request: &timeout},
				},
				{
					Matches: []gatewayapiv1.HTTPRouteMatch{
						{
							Headers: []gatewayapiv1.HTTPHeaderMatch{
								{
									Name:  "X-Gateway-Model-Name",
									Type:  &headerType,
									Value: targetModel,
								},
							},
						},
					},
					BackendRefs: backendRefs,
					Filters:     filters,
					Timeouts:    &gatewayapiv1.HTTPRouteTimeouts{Request: &timeout},
				},
			},
		},
	}
}
