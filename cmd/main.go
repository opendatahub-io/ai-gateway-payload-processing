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
	"strconv"

	ctrl "sigs.k8s.io/controller-runtime"
	"github.com/llm-d/llm-d-inference-payload-processor/cmd/runner"

	ctrlcommon "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/controller/common"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}

func main() {
	// Register ai-gateway payload processing plugins with pluggable bbr
	plugins.RegisterPlugins()

	r := runner.NewRunner().
		WithExecutableName("ai-gateway-payload-processing")

	if os.Getenv("DISABLE_EXTERNAL_MODEL_CONTROLLER") != "true" {
		r = r.WithCustomControllers(
			providerController(),
			modelController(
				envOr("GATEWAY_NAME", ctrlcommon.DefaultGatewayName),
				envOr("GATEWAY_NAMESPACE", ctrlcommon.DefaultGatewayNamespace),
				envOr("DEFAULT_TENANT_NAMESPACE", ctrlcommon.DefaultTenantNamespace),
				envOr("AITENANT_NAMESPACE", ctrlcommon.DefaultAITenantNamespace),
				envBool("ENABLE_TENANT_NAMESPACE_DISCOVERY", true),
			),
			legacyMigrationController(),
		)
	} else {
		ctrl.Log.WithName("setup").Info("ExternalModel/ExternalProvider controllers disabled via DISABLE_EXTERNAL_MODEL_CONTROLLER=true")
	}

	if err := r.Run(ctrl.SetupSignalHandler()); err != nil {
		os.Exit(1)
	}
}
