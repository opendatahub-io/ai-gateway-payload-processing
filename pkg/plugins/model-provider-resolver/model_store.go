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
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// externalModelInfo holds the provider and secret name/namespace for an external model.
type externalModelInfo struct {
	provider        string
	targetModel     string // this is the name of the model that will be used in the request
	secretName      string
	secretNamespace string
}

// modelInfoStore is a thread-safe in-memory store that maps model names to their provider info.
// The reconciler writes to it; the plugin reads from it during request processing.
type modelInfoStore struct {
	// maasModelRefToExternalModel maps a MaaSModelRef CR to ExternalModel CR
	maasModelRefToExternalModel map[types.NamespacedName]types.NamespacedName
	//externalModelToModelInfo maps externalModel CR to externalModelInfo
	externalModelToModelInfo map[types.NamespacedName]*externalModelInfo

	lock sync.RWMutex
}

func newModelInfoStore() *modelInfoStore {
	return &modelInfoStore{
		maasModelRefToExternalModel: make(map[types.NamespacedName]types.NamespacedName),
		externalModelToModelInfo:    make(map[types.NamespacedName]*externalModelInfo),
	}
}

// addOrUpdateMaaSModelRef stores mapping between MaaSModelRef to ExternalModel
func (s *modelInfoStore) addOrUpdateMaaSModelRef(maasModelRefKey types.NamespacedName, externalModelKey types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.maasModelRefToExternalModel[maasModelRefKey] = externalModelKey
}

// deleteMaaSModelRef deletes the mapping between MaaSModelRef to ExternalModel
func (s *modelInfoStore) deleteMaaSModelRef(maasModelRefKey types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.maasModelRefToExternalModel, maasModelRefKey)
}

// addOrUpdateExternalModel stores ExternalModel information.
func (s *modelInfoStore) addOrUpdateExternalModel(externalModelKey types.NamespacedName, modelInfo *externalModelInfo) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.externalModelToModelInfo[externalModelKey] = modelInfo
}

// deleteExternalModel deletes ExternalModel information.
func (s *modelInfoStore) deleteExternalModel(externalModelKey types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.externalModelToModelInfo, externalModelKey)
}

// getModelInfo returns the modelInfo pointed by MaaSModelRef and bool if found or not.
// if no externalModelInfo found, nil is returned in the return value.
func (s *modelInfoStore) getModelInfo(maasModelRefKey types.NamespacedName) (*externalModelInfo, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	externalModelKey, ok := s.maasModelRefToExternalModel[maasModelRefKey]
	if !ok {
		return nil, false // MaaSModelRef not found
	}

	externalModelInfo, ok := s.externalModelToModelInfo[externalModelKey]
	if !ok {
		return nil, false // ExternalModel not found
	}

	return externalModelInfo, true
}
