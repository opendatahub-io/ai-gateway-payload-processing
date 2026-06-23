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

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/apiformat"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/common/auth"
	"k8s.io/apimachinery/pkg/types"
)

// providerInfo holds cached ExternalProvider state.
type providerInfo struct {
	provider        string
	endpoint        string
	path            string
	auth            auth.Auth
	secretName      string
	secretNamespace string
	config          map[string]string
}

// resolvedProviderRef holds resolved provider info for a single ExternalProviderRef.
type resolvedProviderRef struct {
	provider        string
	targetModel     string
	apiFormat       apiformat.APIFormat
	auth            auth.Auth
	endpoint        string
	path            string
	secretName      string
	secretNamespace string
	config          map[string]string
	weight          int
}

// externalModelInfo holds all resolved provider refs for an external model.
// The plugin selects a provider based on weights at request time.
type externalModelInfo struct {
	modelName string
	refs      []*resolvedProviderRef
}

// infoStore is a thread-safe in-memory store for both provider and model info.
// The reconcilers write to it; the plugin reads from it during request processing.
type infoStore struct {
	providers map[string]*providerInfo
	models    map[string]map[string]*externalModelInfo // namespace -> name -> info
	lock      sync.RWMutex
}

func newInfoStore() *infoStore {
	return &infoStore{
		providers: make(map[string]*providerInfo),
		models:    make(map[string]map[string]*externalModelInfo),
	}
}

// addOrUpdateProvider stores ExternalProvider information.
func (s *infoStore) addOrUpdateProvider(key types.NamespacedName, info *providerInfo) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.providers[key.String()] = info
}

// deleteProvider removes ExternalProvider information.
func (s *infoStore) deleteProvider(key types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()
	delete(s.providers, key.String())
}

// getProvider returns provider info if found.
func (s *infoStore) getProvider(key types.NamespacedName) (*providerInfo, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	info, ok := s.providers[key.String()]
	return info, ok
}

// addOrUpdateModel stores ExternalModel information.
func (s *infoStore) addOrUpdateModel(key types.NamespacedName, info *externalModelInfo) {
	s.lock.Lock()
	defer s.lock.Unlock()
	if _, found := s.models[key.Namespace]; !found {
		s.models[key.Namespace] = make(map[string]*externalModelInfo)
	}
	s.models[key.Namespace][key.Name] = info
}

// deleteModel removes ExternalModel information.
func (s *infoStore) deleteModel(key types.NamespacedName) {
	s.lock.Lock()
	defer s.lock.Unlock()
	modelsByNamespace, found := s.models[key.Namespace]
	if !found {
		return
	}
	delete(modelsByNamespace, key.Name)
	if len(modelsByNamespace) == 0 {
		delete(s.models, key.Namespace)
	}
}

// getModel returns model info if found.
func (s *infoStore) getModel(key types.NamespacedName) (*externalModelInfo, bool) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	modelsByNamespace, found := s.models[key.Namespace]
	if !found {
		return nil, false
	}

	info, ok := modelsByNamespace[key.Name]
	return info, ok
}
