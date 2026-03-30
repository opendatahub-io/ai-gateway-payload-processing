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
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// testSecret builds a labeled corev1.Secret for use in reconciler and store tests.
// The credentials map contains field names to values (e.g., "api-key" -> "sk-xxx").
func testSecret(namespace, name string, credentials map[string]string) *corev1.Secret {
	if namespace == "" {
		namespace = "default"
	}
	data := make(map[string][]byte)
	for k, v := range credentials {
		data[k] = []byte(v)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{managedLabel: "true"},
		},
		Data: data,
	}
}

func TestSecretStore(t *testing.T) {
	tests := []struct {
		name          string
		initStoreFunc func(s *secretStore)
		secretKey     string
		wantFound     bool
		wantCreds     map[string]string
	}{
		{
			name: "AddOrUpdate and Get returns stored credentials",
			initStoreFunc: func(s *secretStore) {
				_ = s.addOrUpdate("default/openai-key", testSecret("default", "openai-key", map[string]string{"api-key": "sk-key-1"}))
			},
			secretKey: "default/openai-key",
			wantFound: true,
			wantCreds: map[string]string{"api-key": "sk-key-1"},
		},
		{
			name:          "get nonexistent key returns not found",
			initStoreFunc: func(s *secretStore) {},
			secretKey:     "default/nonexistent",
			wantFound:     false,
		},
		{
			name: "AddOrUpdate overwrites existing entry",
			initStoreFunc: func(s *secretStore) {
				_ = s.addOrUpdate("default/key", testSecret("default", "key", map[string]string{"api-key": "old-key"}))
				_ = s.addOrUpdate("default/key", testSecret("default", "key", map[string]string{"api-key": "new-key"}))
			},
			secretKey: "default/key",
			wantFound: true,
			wantCreds: map[string]string{"api-key": "new-key"},
		},
		{
			name: "delete removes entry",
			initStoreFunc: func(s *secretStore) {
				_ = s.addOrUpdate("default/key", testSecret("default", "key", map[string]string{"api-key": "sk-key-1"}))
				s.delete("default/key")
			},
			secretKey: "default/key",
			wantFound: false,
		},
		{
			name: "delete nonexistent key is a no-op",
			initStoreFunc: func(s *secretStore) {
				s.delete("default/nonexistent")
			},
			secretKey: "default/nonexistent",
			wantFound: false,
		},
		{
			name: "stores multiple fields from secret",
			initStoreFunc: func(s *secretStore) {
				_ = s.addOrUpdate("default/bedrock-creds", testSecret("default", "bedrock-creds", map[string]string{
					"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
					"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				}))
			},
			secretKey: "default/bedrock-creds",
			wantFound: true,
			wantCreds: map[string]string{
				"aws-access-key-id":     "AKIAIOSFODNN7EXAMPLE",
				"aws-secret-access-key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSecretStore()
			tt.initStoreFunc(s)

			creds, found := s.get(tt.secretKey)
			require.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				if diff := cmp.Diff(tt.wantCreds, creds, cmpopts.SortMaps(func(a, b string) bool { return a < b })); diff != "" {
					t.Errorf("credentials mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

// TestAddOrUpdateErrors tests error handling in addOrUpdate.
func TestAddOrUpdateErrors(t *testing.T) {
	tests := []struct {
		name   string
		secret *corev1.Secret
		key    string
	}{
		{
			name: "returns error when secret has no data",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "no-data", Namespace: "default"},
				Data:       map[string][]byte{},
			},
			key: "default/no-data",
		},
		{
			name: "returns error when field is empty",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "empty-key", Namespace: "default"},
				Data:       map[string][]byte{"api-key": []byte("")},
			},
			key: "default/empty-key",
		},
		{
			name: "returns error when one of multiple fields is empty",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "partial", Namespace: "default"},
				Data: map[string][]byte{
					"aws-access-key-id":     []byte("AKIAIOSFODNN7EXAMPLE"),
					"aws-secret-access-key": []byte(""),
				},
			},
			key: "default/partial",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSecretStore()

			err := s.addOrUpdate(tt.key, tt.secret)

			require.Error(t, err)
			_, found := s.get(tt.key)
			assert.False(t, found, "store should not contain entry when addOrUpdate fails")
		})
	}
}

func TestSecretStoreConcurrentAccess(t *testing.T) {
	s := newSecretStore()
	var wg sync.WaitGroup
	goroutines := 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := fmt.Sprintf("default/secret-%d", n)
			sec := testSecret("default", fmt.Sprintf("secret-%d", n), map[string]string{"api-key": "key"})
			_ = s.addOrUpdate(key, sec)
			s.get(key)
			s.delete(key)
		}(i)
	}
	wg.Wait()
}
