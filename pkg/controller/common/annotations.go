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

package common

import (
	"fmt"
	"strconv"
)

const (
	// AnnotationPort is the annotation key for overriding the backend port.
	// Defaults to 443. Valid range: 1-65535.
	AnnotationPort = "inference.opendatahub.io/port"

	// AnnotationTLS is the annotation key for enabling/disabling TLS origination.
	// Defaults to "true". Accepts "true" or "false".
	AnnotationTLS = "inference.opendatahub.io/tls"

	// LegacyAnnotationPort is the legacy annotation key for port from the
	// maas.opendatahub.io/v1alpha1 ExternalModel CRD.
	LegacyAnnotationPort = "maas.opendatahub.io/port"

	// LegacyAnnotationTLS is the legacy annotation key for TLS from the
	// maas.opendatahub.io/v1alpha1 ExternalModel CRD.
	LegacyAnnotationTLS = "maas.opendatahub.io/tls"

	// DefaultTLSEnabled is the default TLS setting when no annotation is present.
	DefaultTLSEnabled = true
)

// ConnectionSettings holds the port and TLS configuration for an external provider.
type ConnectionSettings struct {
	Port       int32
	TLSEnabled bool
}

// ValidateConnectionSecurity returns an error if plaintext transport is used
// with credential-bearing auth types (e.g. "apikey", "sigv4"), which would
// expose secrets to network observers (CWE-319).
func ValidateConnectionSecurity(settings ConnectionSettings, authType string) error {
	if !settings.TLSEnabled && (authType == "apikey" || authType == "sigv4") {
		return fmt.Errorf("plaintext transport (TLS disabled) is not allowed with auth type %q: credentials would be transmitted in cleartext", authType)
	}
	return nil
}

// GetConnectionSettings reads port and TLS settings from annotations.
// Defaults to port 443 (DefaultTLSPort) and TLS enabled.
func GetConnectionSettings(annotations map[string]string) (ConnectionSettings, error) {
	settings := ConnectionSettings{
		Port:       DefaultTLSPort,
		TLSEnabled: DefaultTLSEnabled,
	}

	if annotations == nil {
		return settings, nil
	}

	if portStr, ok := annotations[AnnotationPort]; ok && portStr != "" {
		port, err := strconv.ParseInt(portStr, 10, 32)
		if err != nil || port < 1 || port > 65535 {
			return settings, fmt.Errorf("invalid %s annotation %q: must be an integer between 1 and 65535", AnnotationPort, portStr)
		}
		settings.Port = int32(port)
	}

	if tlsStr, ok := annotations[AnnotationTLS]; ok && tlsStr != "" {
		switch tlsStr {
		case "true":
			settings.TLSEnabled = true
		case "false":
			settings.TLSEnabled = false
		default:
			return settings, fmt.Errorf("invalid %s annotation %q: must be \"true\" or \"false\"", AnnotationTLS, tlsStr)
		}
	}

	return settings, nil
}
