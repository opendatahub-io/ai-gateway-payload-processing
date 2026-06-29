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

package plugins

import (
	api_translation "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/api-translation"
	apikey_injection "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/apikey-injection"
	maas_headers_guard "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/maas-headers-guard"
	provider_resolver "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/model-provider-resolver"
	"github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/nemo"
	stream_usage_enforcer "github.com/opendatahub-io/ai-gateway-payload-processing/pkg/plugins/stream-usage-enforcer"
	"github.com/llm-d/llm-d-inference-payload-processor/pkg/framework/interface/plugin"
)

func RegisterPlugins() {
	plugin.Register(maas_headers_guard.PluginType, maas_headers_guard.Factory)
	plugin.Register(provider_resolver.ModelProviderResolverPluginType, provider_resolver.ModelProviderResolverFactory)
	plugin.Register(api_translation.APITranslationPluginType, api_translation.APITranslationFactory)
	plugin.Register(apikey_injection.APIKeyInjectionPluginType, apikey_injection.APIKeyInjectionFactory)
	plugin.Register(nemo.NemoRequestGuardPluginType, nemo.NemoRequestGuardFactory)
	plugin.Register(nemo.NemoResponseGuardPluginType, nemo.NemoResponseGuardFactory)
	plugin.Register(stream_usage_enforcer.PluginType, stream_usage_enforcer.Factory)
}
