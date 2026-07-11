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

	logutil "github.com/llm-d/llm-d-inference-payload-processor/pkg/common/observability/logging"
)

var llmInferenceServiceGVK = schema.GroupVersionKind{
	Group:   "serving.kserve.io",
	Version: "v1alpha1",
	Kind:    "LLMInferenceService",
}

// llmisvcReconciler watches KServe LLMInferenceService CRDs and maintains
// a mapping from spec.model.name to the publisher ID used in BBR HTTPRoute
// header matching. Uses unstructured client to avoid importing kserve types.
type llmisvcReconciler struct {
	client.Reader
	store *infoStore
}

func (r *llmisvcReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).V(logutil.DEFAULT)
	logger.Info("reconciling LLMInferenceService", "name", req.Name, "namespace", req.Namespace)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(llmInferenceServiceGVK)
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if errors.IsNotFound(err) {
			r.store.deleteLLMISvcByKey(req.NamespacedName.String())
			logger.Info("LLMInferenceService removed from store", "name", req.Name, "namespace", req.Namespace)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("unable to get LLMInferenceService: %w", err)
	}

	modelName := extractModelName(obj)
	publisherID := fmt.Sprintf("publishers/%s/models/%s", req.Namespace, modelName)

	r.store.addOrUpdateLLMISvc(modelName, &llmisvcModelInfo{
		modelName:   modelName,
		publisherID: publisherID,
		key:         req.NamespacedName.String(),
	})
	logger.Info("updated LLMInferenceService store", "modelName", modelName, "publisherID", publisherID)
	return ctrl.Result{}, nil
}

// extractModelName returns spec.model.name if set, otherwise metadata.name.
func extractModelName(obj *unstructured.Unstructured) string {
	name, found, err := unstructured.NestedString(obj.Object, "spec", "model", "name")
	if err == nil && found && name != "" {
		return name
	}
	return obj.GetName()
}

// newLLMISvcWatchObject returns an unstructured object with the correct GVK
// for the controller-runtime builder to watch.
func newLLMISvcWatchObject() *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(llmInferenceServiceGVK)
	return obj
}
