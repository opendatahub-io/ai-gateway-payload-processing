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

package v1alpha1

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func inferenceCRDs(t *testing.T) []*apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	var crds []*apiextensionsv1.CustomResourceDefinition
	for _, plural := range []string{"externalmodels", "externalproviders"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "config", "crd", "bases", "inference.opendatahub.io_"+plural+".yaml"))
		require.NoError(t, err)
		crd := &apiextensionsv1.CustomResourceDefinition{}
		require.NoError(t, yaml.UnmarshalStrict(data, crd))
		crds = append(crds, crd)
	}
	return crds
}

func stripDescriptions(schema *apiextensionsv1.JSONSchemaProps) {
	schema.Description = ""
	for name, property := range schema.Properties {
		stripDescriptions(&property)
		schema.Properties[name] = property
	}
	if schema.Items != nil && schema.Items.Schema != nil {
		stripDescriptions(schema.Items.Schema)
	}
	if schema.AdditionalProperties != nil && schema.AdditionalProperties.Schema != nil {
		stripDescriptions(schema.AdditionalProperties.Schema)
	}
}

// Pin the AGC-generated contract independently of this repo's local API mirror.
// See testdata/README.md for provenance and the coordinated update procedure.
func TestAGCSchemaMirror(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "agc-schema.json"))
	require.NoError(t, err)
	var expected map[string]apiextensionsv1.JSONSchemaProps
	require.NoError(t, json.Unmarshal(data, &expected))
	crds := inferenceCRDs(t)
	require.Len(t, expected, len(crds))
	for _, crd := range crds {
		t.Run(crd.Spec.Names.Kind, func(t *testing.T) {
			actual := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.DeepCopy()
			stripDescriptions(actual)
			assert.Equal(t, expected[crd.Spec.Names.Plural], *actual, "local schema drifted from the AGC contract")
		})
	}
}
