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

package auth

import (
	"fmt"
)

// compile-time interface check
var _ AuthHeadersGenerator = &SimpleAuthGenerator{}

// SimpleAuthGenerator generates a single auth header from an API key.
// HeaderName is the HTTP header (e.g. "Authorization", "x-api-key").
// HeaderValuePrefix is prepended to the key (e.g. "Bearer "); use "" for raw keys.
type SimpleAuthGenerator struct {
	HeaderName        string
	HeaderValuePrefix string
}

// GenerateAuthHeaders returns the header name and formatted value for the given API key.
func (g *SimpleAuthGenerator) GenerateAuthHeaders(apiKey string) map[string]string {
	return map[string]string{
		g.HeaderName: fmt.Sprintf("%s%s", g.HeaderValuePrefix, apiKey),
	}
}
