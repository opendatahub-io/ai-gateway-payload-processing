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

// apiKeyField is the field name in the credentials map that holds the API key.
const apiKeyField = "api-key"

// compile-time interface check
var _ AuthHeadersGenerator = &SimpleAuthGenerator{}

// SimpleAuthGenerator generates a single auth header from an API key.
// HeaderName is the HTTP header (e.g. "Authorization", "x-api-key").
// HeaderValuePrefix is prepended to the key (e.g. "Bearer "); use "" for raw keys.
// Requires the credentials map to contain an "api-key" field.
type SimpleAuthGenerator struct {
	HeaderName        string
	HeaderValuePrefix string
}

// GenerateAuthHeaders extracts the "api-key" field from credentials and returns
// the header name and formatted value. Returns an error if the field is missing.
func (g *SimpleAuthGenerator) GenerateAuthHeaders(credentials map[string]string) (map[string]string, error) {
	apiKey, ok := credentials[apiKeyField]
	if !ok {
		return nil, fmt.Errorf("credentials missing required field %s", apiKeyField)
	}

	return map[string]string{
		g.HeaderName: fmt.Sprintf("%s%s", g.HeaderValuePrefix, apiKey),
	}, nil
}
