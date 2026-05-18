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

type providerInfo struct {
	provider        string
	endpoint        string
	secretName      string
	secretNamespace string
	config          map[string]string
}

type providerInfoStore struct {
	providers map[string]*providerInfo
	lock      sync.RWMutex
}

func newProviderInfoStore() *providerInfoStore {
	return &providerInfoStore{
		providers: make(map[string]*providerInfo),
	}
}

func (s *providerInfoStore) addOrUpdate(key types.NamespacedName, info *providerInfo) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.providers[key.String()] = info
}

func (s *providerInfoStore) delete(key types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.providers, key.String())
}

func (s *providerInfoStore) get(key types.NamespacedName) (*providerInfo, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	info, ok := s.providers[key.String()]
	return info, ok
}

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
	// externalModelToModelInfo maps namespace -> external model name -> externalModelInfo.
	externalModelToModelInfo map[string]map[string]*externalModelInfo

	lock sync.RWMutex
}

func newModelInfoStore() *modelInfoStore {
	return &modelInfoStore{
		externalModelToModelInfo: make(map[string]map[string]*externalModelInfo),
	}
}

func (s *modelInfoStore) addOrUpdateExternalModel(externalModelKey types.NamespacedName, modelInfo *externalModelInfo) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, found := s.externalModelToModelInfo[externalModelKey.Namespace]; !found {
		s.externalModelToModelInfo[externalModelKey.Namespace] = make(map[string]*externalModelInfo)
	}
	s.externalModelToModelInfo[externalModelKey.Namespace][externalModelKey.Name] = modelInfo
}

func (s *modelInfoStore) deleteExternalModel(externalModelKey types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()
	modelsByNamespace, found := s.externalModelToModelInfo[externalModelKey.Namespace]
	if !found {
		return
	}
	delete(modelsByNamespace, externalModelKey.Name)
	if len(modelsByNamespace) == 0 {
		delete(s.externalModelToModelInfo, externalModelKey.Namespace)
	}
}

func (s *modelInfoStore) getModelInfo(externalModelKey types.NamespacedName) (*externalModelInfo, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	modelsByNamespace, found := s.externalModelToModelInfo[externalModelKey.Namespace]
	if !found {
		return nil, false
	}

	externalModelInfo, ok := modelsByNamespace[externalModelKey.Name]
	if !ok {
		return nil, false
	}

	return externalModelInfo, true
}
