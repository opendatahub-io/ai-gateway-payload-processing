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

type Auth string

const (
	APIKey Auth = "apikey"
	SigV4  Auth = "sigv4"
	OAuth2 Auth = "oauth2" // not used yet
	None   Auth = "none"
)

const (
	APIKeyAuthHeaderName  = "authHeaderName"
	APIKeyAuthValuePrefix = "authValuePrefix"
)
