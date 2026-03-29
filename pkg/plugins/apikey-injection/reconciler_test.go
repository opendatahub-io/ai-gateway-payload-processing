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
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcile(t *testing.T) {
	tests := []struct {
		name        string
		initSecrets []*corev1.Secret
		secret      *corev1.Secret
		wantKey     string
		wantCreds   map[string]string
		wantFound   bool
		wantErr     bool
	}{
		{
			name:      "stores credentials from Secret",
			secret:    testSecret("default", "openai-key", map[string]string{"api-key": "sk-live-xxx"}),
			wantKey:   "default/openai-key",
			wantCreds: map[string]string{"api-key": "sk-live-xxx"},
			wantFound: true,
		},
		{
			name:        "updates existing entry on Secret change",
			initSecrets: []*corev1.Secret{testSecret("default", "openai-key", map[string]string{"api-key": "sk-old-key"})},
			secret:      testSecret("default", "openai-key", map[string]string{"api-key": "sk-new-key"}),
			wantKey:     "default/openai-key",
			wantCreds:   map[string]string{"api-key": "sk-new-key"},
			wantFound:   true,
		},
		{
			name:        "Secret not found — cleans store",
			initSecrets: []*corev1.Secret{testSecret("default", "gone", map[string]string{"api-key": "sk-key"})},
			secret:      nil,
			wantKey:     "default/gone",
			wantFound:   false,
		},
		{
			name: "Secret with no data — returns error",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-data",
					Namespace: "default",
					Labels:    map[string]string{managedLabel: "true"},
				},
				Data: map[string][]byte{},
			},
			wantKey:   "default/no-data",
			wantFound: false,
			wantErr:   true,
		},
		{
			name:        "Secret marked for deletion — removes from store",
			initSecrets: []*corev1.Secret{testSecret("default", "deleting", map[string]string{"api-key": "sk-key"})},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deleting",
					Namespace:         "default",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
					Finalizers:        []string{"test-finalizer"},
					Labels:            map[string]string{managedLabel: "true"},
				},
				Data: map[string][]byte{
					"api-key": []byte("sk-key"),
				},
			},
			wantKey:   "default/deleting",
			wantFound: false,
		},
		{
			name:        "Secret with managed label removed — removes from store",
			initSecrets: []*corev1.Secret{testSecret("default", "unlabeled", map[string]string{"api-key": "sk-key"})},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "unlabeled",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"api-key": []byte("sk-key"),
				},
			},
			wantKey:   "default/unlabeled",
			wantFound: false,
		},
		{
			name:      "stores multiple credential fields",
			secret:    testSecret("default", "bedrock-creds", map[string]string{"aws-access-key-id": "AKIA...", "aws-secret-access-key": "secret"}),
			wantKey:   "default/bedrock-creds",
			wantCreds: map[string]string{"aws-access-key-id": "AKIA...", "aws-secret-access-key": "secret"},
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newSecretStore()
			for _, sec := range tt.initSecrets {
				_ = store.addOrUpdate(fmt.Sprintf("%s/%s", sec.Namespace, sec.Name), sec)
			}

			builder := fake.NewClientBuilder()
			if tt.secret != nil {
				builder = builder.WithObjects(tt.secret)
			}
			fakeClient := builder.Build()

			reconciler := &secretReconciler{
				Reader: fakeClient,
				store:  store,
			}

			parts := strings.Split(tt.wantKey, "/")
			_, err := reconciler.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: parts[0],
					Name:      parts[1],
				},
			})

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tt.wantKey != "" {
				creds, found := store.get(tt.wantKey)
				assert.Equal(t, tt.wantFound, found)
				if tt.wantFound {
					if diff := cmp.Diff(tt.wantCreds, creds); diff != "" {
						t.Errorf("credentials mismatch (-want +got):\n%s", diff)
					}
				}
			}
		})
	}
}
