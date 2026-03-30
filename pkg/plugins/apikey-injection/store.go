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
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
)

// secretStore is a thread-safe in-memory store that maps a Secret's
// namespaced name ("namespace/name") to its credential data.
// The secretReconciler writes to it; the apiKeyInjectionPlugin reads from it.
type secretStore struct {
	mu   sync.RWMutex
	data map[string]map[string]string
}

// newSecretStore creates an empty secretStore.
func newSecretStore() *secretStore {
	return &secretStore{
		data: make(map[string]map[string]string),
	}
}

// addOrUpdate extracts all fields from the Secret's data and stores them under
// the given key. Returns an error if the Secret has no data fields or if any
// field is empty.
func (s *secretStore) addOrUpdate(key string, secret *corev1.Secret) error {
	if len(secret.Data) == 0 {
		s.delete(key)
		return fmt.Errorf("secret '%s' has no data fields", key)
	}

	credentials := make(map[string]string)
	for field, value := range secret.Data {
		if len(value) == 0 {
			s.delete(key)
			return fmt.Errorf("secret '%s' has empty field '%s'", key, field)
		}
		credentials[field] = string(value)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = credentials
	return nil
}

// delete removes the entry for the given Secret namespaced name.
func (s *secretStore) delete(secretKey string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, secretKey)
}

// get returns the credentials for the given namespaced name and whether it was found.
func (s *secretStore) get(secretKey string) (map[string]string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	credentials, ok := s.data[secretKey]
	return credentials, ok
}
