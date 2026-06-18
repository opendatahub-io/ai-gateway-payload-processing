/*
Copyright 2025 The Kubernetes Authors.

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

/**
 * This file is adapted from Gateway API Inference Extension
 * Original source: https://github.com/kubernetes-sigs/gateway-api-inference-extension/blob/main/cmd/bbr/main.go
 * Licensed under the Apache License, Version 2.0
 */

package main

import (
	"os"

	ctrl "sigs.k8s.io/controller-runtime"
	"github.com/llm-d/llm-d-inference-payload-processor/cmd/runner"

	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins"
)

func main() {
	// Register ai-gateway payload processing plugins with pluggable bbr
	plugins.RegisterPlugins()

	if err := runner.NewRunner().
		WithExecutableName("ai-gateway-payload-processing").
		WithCustomControllers(
			providerController(),
			modelController(
				os.Getenv("GATEWAY_NAME"),
				os.Getenv("GATEWAY_NAMESPACE"),
			),
			legacyMigrationController(),
		).
		Run(ctrl.SetupSignalHandler()); err != nil {
		os.Exit(1)
	}
}
