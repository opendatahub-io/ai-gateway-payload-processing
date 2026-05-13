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

package authgenerator

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"golang.org/x/oauth2"
)

func TestGCPOAuth2Generator(t *testing.T) {
	tests := []struct {
		name            string
		credentials     map[string]string
		wantHeaders     map[string]string
		wantErrContains string
	}{
		{
			name: "valid credentials returns Authorization header",
			credentials: map[string]string{
				gcpServiceAccountField: "", // will be replaced with mock server URL
			},
			wantHeaders: map[string]string{
				"Authorization": "Bearer test-token-123",
			},
		},
		{
			name:            "missing credentials field returns error",
			credentials:     map[string]string{"wrong-field": "some-value"},
			wantErrContains: gcpServiceAccountField,
		},
		{
			name:            "empty credentials returns error",
			credentials:     map[string]string{},
			wantErrContains: gcpServiceAccountField,
		},
		{
			name: "invalid JSON returns error",
			credentials: map[string]string{
				gcpServiceAccountField: "not-valid-json",
			},
			wantErrContains: "failed to parse service account JSON",
		},
		{
			name: "invalid service account type returns error",
			credentials: map[string]string{
				gcpServiceAccountField: `{"type": "wrong_type"}`,
			},
			wantErrContains: "failed to parse service account JSON",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var mockServer *httptest.Server

			// Set up mock server for tests that need valid credentials
			if test.wantHeaders != nil {
				mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{
						"access_token": "test-token-123",
						"token_type":   "Bearer",
						"expires_in":   3600,
					})
				}))
				defer mockServer.Close()
				test.credentials[gcpServiceAccountField] = createTestServiceAccountJSON(t, mockServer.URL)
			}

			generator := NewGCPOAuth2Generator()
			gotHeaders, err := generator.GenerateAuthHeaders(test.credentials)

			if test.wantErrContains != "" {
				if err == nil {
					t.Errorf("expected error containing %q but got nil", test.wantErrContains)
					return
				}
				if !strings.Contains(err.Error(), test.wantErrContains) {
					t.Errorf("error %q does not contain %q", err.Error(), test.wantErrContains)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if diff := cmp.Diff(test.wantHeaders, gotHeaders, cmpopts.SortMaps(func(a, b string) bool { return a < b })); diff != "" {
				t.Errorf("headers mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGCPOAuth2Generator_TokenCaching(t *testing.T) {
	tokenCallCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCallCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-token-123",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer mockServer.Close()

	serviceAccountJSON := createTestServiceAccountJSON(t, mockServer.URL)
	generator := NewGCPOAuth2Generator()
	credentials := map[string]string{
		gcpServiceAccountField: serviceAccountJSON,
	}

	// First call should fetch token
	headers1, err := generator.GenerateAuthHeaders(credentials)
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if headers1["Authorization"] != "Bearer test-token-123" {
		t.Errorf("first call: got %q, want %q", headers1["Authorization"], "Bearer test-token-123")
	}
	if tokenCallCount != 1 {
		t.Errorf("first call: token fetch count = %d, want 1", tokenCallCount)
	}

	// Second call should use cached token
	headers2, err := generator.GenerateAuthHeaders(credentials)
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}
	if headers2["Authorization"] != "Bearer test-token-123" {
		t.Errorf("second call: got %q, want %q", headers2["Authorization"], "Bearer test-token-123")
	}
	if tokenCallCount != 1 {
		t.Errorf("second call: token fetch count = %d, want 1 (should use cache)", tokenCallCount)
	}
}

func TestGCPOAuth2Generator_DifferentCredentialsGetDifferentCacheEntries(t *testing.T) {
	callCount := 0
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "token-" + string(rune('A'+callCount-1)),
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer mockServer.Close()

	generator := NewGCPOAuth2Generator()

	// First credentials
	creds1 := map[string]string{
		gcpServiceAccountField: createTestServiceAccountJSONWithEmail(t, mockServer.URL, "service1@project.iam.gserviceaccount.com"),
	}
	if _, err := generator.GenerateAuthHeaders(creds1); err != nil {
		t.Fatalf("first credentials failed: %v", err)
	}

	// Second credentials (different service account)
	creds2 := map[string]string{
		gcpServiceAccountField: createTestServiceAccountJSONWithEmail(t, mockServer.URL, "service2@project.iam.gserviceaccount.com"),
	}
	if _, err := generator.GenerateAuthHeaders(creds2); err != nil {
		t.Fatalf("second credentials failed: %v", err)
	}

	// Should have made 2 calls (one per unique credentials)
	if callCount != 2 {
		t.Errorf("token fetch count = %d, want 2 (one per unique credentials)", callCount)
	}

	// Verify cache has 2 separate entries
	if len(generator.cache) != 2 {
		t.Errorf("cache size = %d, want 2 (one per unique credentials)", len(generator.cache))
	}
}

func TestGCPOAuth2Generator_ConcurrentAccess(t *testing.T) {
	tokenCallCount := 0
	var mu sync.Mutex

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokenCallCount++
		mu.Unlock()

		time.Sleep(10 * time.Millisecond) // simulate network delay

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "concurrent-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	}))
	defer mockServer.Close()

	serviceAccountJSON := createTestServiceAccountJSON(t, mockServer.URL)
	generator := NewGCPOAuth2Generator()
	credentials := map[string]string{
		gcpServiceAccountField: serviceAccountJSON,
	}

	// Launch multiple goroutines
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			headers, err := generator.GenerateAuthHeaders(credentials)
			if err != nil {
				errors <- err
				return
			}
			if headers["Authorization"] != "Bearer concurrent-token" {
				t.Errorf("got %q, want %q", headers["Authorization"], "Bearer concurrent-token")
			}
		}()
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("goroutine error: %v", err)
	}

	// Due to double-checked locking, we may get 1-2 calls but definitely not 10
	if tokenCallCount > 2 {
		t.Errorf("token fetch count = %d, want <= 2 (double-checked locking should minimize fetches)", tokenCallCount)
	}
}

func TestGCPOAuth2Generator_IsTokenValid(t *testing.T) {
	tests := []struct {
		name   string
		cached *cachedToken
		want   bool
	}{
		{
			name:   "nil cached token",
			cached: nil,
			want:   false,
		},
		{
			name:   "nil token in cached",
			cached: &cachedToken{token: nil, expiresAt: time.Now().Add(time.Hour)},
			want:   false,
		},
		{
			name:   "expired token",
			cached: &cachedToken{token: &oauth2.Token{}, expiresAt: time.Now().Add(-time.Hour)},
			want:   false,
		},
		{
			name:   "token expiring within 5 minutes",
			cached: &cachedToken{token: &oauth2.Token{}, expiresAt: time.Now().Add(3 * time.Minute)},
			want:   false,
		},
		{
			name:   "valid token with margin",
			cached: &cachedToken{token: &oauth2.Token{}, expiresAt: time.Now().Add(10 * time.Minute)},
			want:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := isTokenValid(test.cached)
			if got != test.want {
				t.Errorf("isTokenValid() = %v, want %v", got, test.want)
			}
		})
	}
}

// createTestServiceAccountJSON creates a minimal service account JSON for testing.
// The token_uri points to the mock server.
func createTestServiceAccountJSON(t *testing.T, tokenURL string) string {
	return createTestServiceAccountJSONWithEmail(t, tokenURL, "test@test-project.iam.gserviceaccount.com")
}

func createTestServiceAccountJSONWithEmail(t *testing.T, tokenURL, email string) string {
	t.Helper()

	sa := map[string]any{
		"type":                        "service_account",
		"project_id":                  "test-project",
		"private_key_id":              "key123",
		"private_key":                 generateTestPrivateKeyPEM(t),
		"client_email":                email,
		"client_id":                   "123456789",
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   tokenURL,
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url":        "https://www.googleapis.com/robot/v1/metadata/x509/test",
	}

	jsonBytes, err := json.Marshal(sa)
	if err != nil {
		t.Fatalf("failed to marshal service account JSON: %v", err)
	}
	return string(jsonBytes)
}

// generateTestPrivateKeyPEM generates a fresh RSA private key for testing.
func generateTestPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}
