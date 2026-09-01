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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetConnectionSettings_Defaults(t *testing.T) {
	settings, err := GetConnectionSettings(nil)
	require.NoError(t, err)
	assert.Equal(t, DefaultTLSPort, settings.Port)
	assert.True(t, settings.TLSEnabled)
}

func TestGetConnectionSettings_EmptyAnnotations(t *testing.T) {
	settings, err := GetConnectionSettings(map[string]string{})
	require.NoError(t, err)
	assert.Equal(t, DefaultTLSPort, settings.Port)
	assert.True(t, settings.TLSEnabled)
}

func TestGetConnectionSettings_CustomPort(t *testing.T) {
	settings, err := GetConnectionSettings(map[string]string{
		AnnotationPort: "8080",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(8080), settings.Port)
	assert.True(t, settings.TLSEnabled)
}

func TestGetConnectionSettings_TLSDisabled(t *testing.T) {
	settings, err := GetConnectionSettings(map[string]string{
		AnnotationTLS: "false",
	})
	require.NoError(t, err)
	assert.Equal(t, DefaultTLSPort, settings.Port)
	assert.False(t, settings.TLSEnabled)
}

func TestGetConnectionSettings_CustomPortAndTLS(t *testing.T) {
	settings, err := GetConnectionSettings(map[string]string{
		AnnotationPort: "8080",
		AnnotationTLS:  "false",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(8080), settings.Port)
	assert.False(t, settings.TLSEnabled)
}

func TestGetConnectionSettings_InvalidPort(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"zero", "0"},
		{"negative", "-1"},
		{"too large", "65536"},
		{"not a number", "abc"},
		{"float", "443.5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GetConnectionSettings(map[string]string{
				AnnotationPort: tt.value,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), AnnotationPort)
		})
	}
}

func TestGetConnectionSettings_InvalidTLS(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{"yes", "yes"},
		{"no", "no"},
		{"numeric zero", "0"},
		{"numeric one", "1"},
		{"short true", "t"},
		{"short false", "f"},
		{"upper TRUE", "TRUE"},
		{"upper FALSE", "FALSE"},
		{"upper T", "T"},
		{"upper F", "F"},
		{"mixed case True", "True"},
		{"mixed case False", "False"},
		{"garbage", "notabool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GetConnectionSettings(map[string]string{
				AnnotationTLS: tt.value,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), AnnotationTLS)
		})
	}
}

func TestGetConnectionSettings_ValidPortBoundaries(t *testing.T) {
	// Port 1 (minimum)
	settings, err := GetConnectionSettings(map[string]string{
		AnnotationPort: "1",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(1), settings.Port)

	// Port 65535 (maximum)
	settings, err = GetConnectionSettings(map[string]string{
		AnnotationPort: "65535",
	})
	require.NoError(t, err)
	assert.Equal(t, int32(65535), settings.Port)
}

func TestGetConnectionSettings_UnrelatedAnnotations(t *testing.T) {
	settings, err := GetConnectionSettings(map[string]string{
		"some-other-annotation": "value",
		"another/annotation":    "123",
	})
	require.NoError(t, err)
	assert.Equal(t, DefaultTLSPort, settings.Port)
	assert.True(t, settings.TLSEnabled)
}
