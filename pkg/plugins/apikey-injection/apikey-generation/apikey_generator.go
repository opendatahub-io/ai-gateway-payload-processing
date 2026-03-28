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

package apikey_generation

import "fmt"

// ApiKeyGenerator generates a single auth header from an API key.
type ApiKeyGenerator interface {
	GenerateHeader(apiKey string) (string, string)
}

// compile-time interface check
var _ ApiKeyGenerator = &SimpleApiKeyGenerator{}

// apiKeyGenerator generates a single auth header from an API key.
// headerName is the HTTP header (e.g. "Authorization", "x-api-key").
// headerValuePrefix is prepended to the key (e.g. "Bearer "); use "" for raw keys.
type SimpleApiKeyGenerator struct {
	HeaderName        string
	HeaderValuePrefix string
}

// GenerateHeader returns the header name and formatted value for the given API key.
func (g *SimpleApiKeyGenerator) GenerateHeader(apiKey string) (string, string) {
	return g.HeaderName, fmt.Sprintf("%s%s", g.HeaderValuePrefix, apiKey)
}
