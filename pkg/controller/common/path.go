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
	"regexp"
	"strings"
)

var placeholderRe = regexp.MustCompile(`\{([^}]+)\}`)

// ResolvePath substitutes {key} placeholders in path with values from config
// and returns an error if any placeholders remain unresolved.
func ResolvePath(path string, config map[string]string) (string, error) {
	if path == "" || !strings.Contains(path, "{") {
		return path, nil
	}
	for k, v := range config {
		path = strings.ReplaceAll(path, "{"+k+"}", v)
	}
	matches := placeholderRe.FindAllStringSubmatch(path, -1)
	if len(matches) > 0 {
		keys := make([]string, 0, len(matches))
		for _, m := range matches {
			keys = append(keys, m[1])
		}
		return "", fmt.Errorf("path has unresolved placeholders %v — add these keys to the provider or model config", keys)
	}
	return path, nil
}
