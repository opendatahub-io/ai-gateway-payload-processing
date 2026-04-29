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
	"math/rand"
	"sync"

	"k8s.io/apimachinery/pkg/types"
)

// providerRef holds the resolved info for a single provider ref on an ExternalModel.
type providerRef struct {
	providerName    string
	provider        string
	targetModel     string
	secretName      string
	secretNamespace string
	config          map[string]string
	apiFormat       string
	weight          int32
}

// externalModelInfo holds all provider refs for an external model.
type externalModelInfo struct {
	modelName   string // metadata.name — the client-facing model name
	refs        []providerRef
	totalWeight int32 // precomputed sum of weights
}

// selectProvider picks a provider ref using weighted random selection.
func (m *externalModelInfo) selectProvider() *providerRef {
	if len(m.refs) == 1 {
		return &m.refs[0]
	}
	if m.totalWeight <= 0 {
		return &m.refs[0]
	}

	r := rand.Int31n(m.totalWeight)
	var cumulative int32
	for i := range m.refs {
		cumulative += m.refs[i].weight
		if r < cumulative {
			return &m.refs[i]
		}
	}
	return &m.refs[len(m.refs)-1]
}

// modelInfoStore is a thread-safe in-memory store that maps model names to their provider info.
// The reconciler writes to it; the plugin reads from it during request processing.
type modelInfoStore struct {
	externalModelToModelInfo map[string]*externalModelInfo
	lock                     sync.RWMutex
}

func newModelInfoStore() *modelInfoStore {
	return &modelInfoStore{
		externalModelToModelInfo: make(map[string]*externalModelInfo),
	}
}

// addOrUpdateExternalModel stores ExternalModel information.
func (s *modelInfoStore) addOrUpdateExternalModel(externalModelKey types.NamespacedName, modelInfo *externalModelInfo) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.externalModelToModelInfo[externalModelKey.String()] = modelInfo
}

// deleteExternalModel deletes ExternalModel information.
func (s *modelInfoStore) deleteExternalModel(externalModelKey types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.externalModelToModelInfo, externalModelKey.String())
}

// getModelInfo returns the modelInfo stored in ExternalModel and bool if found or not.
func (s *modelInfoStore) getModelInfo(externalModelKey types.NamespacedName) (*externalModelInfo, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	info, ok := s.externalModelToModelInfo[externalModelKey.String()]
	if !ok {
		return nil, false
	}

	return info, true
}
