package e2e_new_crds

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// Provider represents a provider configuration for testing with the new CRDs.
// Each provider gets an ExternalProvider CR (shared endpoint/creds) and
// an ExternalModel CR (client-facing name + targetModel).
type Provider struct {
	ModelName    string // ExternalModel CR name (client-facing)
	ProviderName string // ExternalProvider CR name
	ProviderType string // provider type (openai, anthropic, etc.)
	SimulatorKey string // API key matching llm-katan defaults
}

var simulatorKeys = map[string]string{
	"openai":         "llm-katan-openai-key",
	"anthropic":      "llm-katan-anthropic-key",
	"azure-openai":   "llm-katan-azure-key",
	"vertex-openai":  "llm-katan-vertexai-key",
	"bedrock-openai": "llm-katan-bedrock-key",
}

var providers = []Provider{
	{ModelName: "e2e-openai", ProviderName: "e2e-prov-openai", ProviderType: "openai", SimulatorKey: simulatorKeys["openai"]},
	{ModelName: "e2e-anthropic", ProviderName: "e2e-prov-anthropic", ProviderType: "anthropic", SimulatorKey: simulatorKeys["anthropic"]},
	{ModelName: "e2e-azure", ProviderName: "e2e-prov-azure", ProviderType: "azure-openai", SimulatorKey: simulatorKeys["azure-openai"]},
	{ModelName: "e2e-bedrock", ProviderName: "e2e-prov-bedrock", ProviderType: "bedrock-openai", SimulatorKey: simulatorKeys["openai"]},
	{ModelName: "e2e-vertex-openai", ProviderName: "e2e-prov-vertex", ProviderType: "vertex-openai", SimulatorKey: simulatorKeys["vertex-openai"]},
}

func createProviderResources(p Provider) {
	// Secret with API key
	kubectlApplyLiteral(fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    inference.networking.k8s.io/bbr-managed: "true"
type: Opaque
stringData:
  api-key: %s
`, p.ProviderName, nsName, p.SimulatorKey))

	// ExternalProvider CR (inference.opendatahub.io/v1alpha1)
	kubectlApplyLiteral(fmt.Sprintf(`
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalProvider
metadata:
  name: %s
  namespace: %s
spec:
  provider: %s
  endpoint: %s
  auth:
    secretRef:
      name: %s
`, p.ProviderName, nsName, p.ProviderType, simulatorEP, p.ProviderName))

	// ExternalModel CR (inference.opendatahub.io/v1alpha1)
	kubectlApplyLiteral(fmt.Sprintf(`
apiVersion: inference.opendatahub.io/v1alpha1
kind: ExternalModel
metadata:
  name: %s
  namespace: %s
spec:
  externalProviderRefs:
    - ref:
        name: %s
      targetModel: %s
`, p.ModelName, nsName, p.ProviderName, p.ModelName))
}

func deleteProviderResources(p Provider) {
	kubectlDeleteResource("externalmodel.inference.opendatahub.io", p.ModelName, nsName)
	kubectlDeleteResource("externalprovider.inference.opendatahub.io", p.ProviderName, nsName)
	kubectlDeleteResource("secret", p.ProviderName, nsName)
}

func buildCurlCommand(modelName string, body map[string]any) []string {
	bodyBytes, _ := json.Marshal(body)

	svcName := gatewayName + "-istio"
	gatewayURL := fmt.Sprintf("http://%s.%s.svc:80/%s/%s/v1/chat/completions",
		svcName, gatewayNs, nsName, modelName)

	return []string{
		"curl", "-si", "--max-time", strconv.Itoa(int(curlTimeout.Seconds())),
		gatewayURL,
		"-H", "Content-Type: application/json",
		"-H", "Connection: close",
		"-d", string(bodyBytes),
	}
}

func getCurlCommand(modelName string) []string {
	return buildCurlCommand(modelName, map[string]any{
		"model":    modelName,
		"messages": []map[string]string{{"role": "user", "content": "hello from " + modelName}},
	})
}

func getCurlCommandWithTools(modelName string) []string {
	return buildCurlCommand(modelName, map[string]any{
		"model":    modelName,
		"messages": []map[string]string{{"role": "user", "content": "whats the weather in paris"}},
		"tools": []map[string]any{
			{
				"type": "function",
				"function": map[string]any{
					"name":        "get_weather",
					"description": "Get weather for a location",
					"parameters": map[string]any{
						"type":       "object",
						"properties": map[string]any{"location": map[string]string{"type": "string"}},
						"required":   []string{"location"},
					},
				},
			},
		},
	})
}

func getCurlCommandWithImage(modelName string) []string {
	return buildCurlCommand(modelName, map[string]any{
		"model": modelName,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": "What is this?"},
					{"type": "image_url", "image_url": map[string]string{
						"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
					}},
				},
			},
		},
	})
}

func getCurlCommandWithJSONMode(modelName string) []string {
	return buildCurlCommand(modelName, map[string]any{
		"model":           modelName,
		"messages":        []map[string]string{{"role": "user", "content": "list 3 colors as JSON"}},
		"response_format": map[string]string{"type": "json_object"},
	})
}

func getCurlCommandWithSystemPrompt(modelName string) []string {
	return buildCurlCommand(modelName, map[string]any{
		"model": modelName,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "hello"},
		},
	})
}

func getCurlCommandMultiTurn(modelName string) []string {
	return buildCurlCommand(modelName, map[string]any{
		"model": modelName,
		"messages": []map[string]string{
			{"role": "user", "content": "my name is test-user"},
			{"role": "assistant", "content": "nice to meet you"},
			{"role": "user", "content": "what is my name"},
		},
	})
}

func getCurlCommandRaw(modelName string, body string) []string {
	svcName := gatewayName + "-istio"
	gatewayURL := fmt.Sprintf("http://%s.%s.svc:80/%s/%s/v1/chat/completions",
		svcName, gatewayNs, nsName, modelName)

	return []string{
		"curl", "-si", "--max-time", strconv.Itoa(int(curlTimeout.Seconds())),
		gatewayURL,
		"-H", "Content-Type: application/json",
		"-H", "Connection: close",
		"-d", body,
	}
}

func filterProviders(providers []Provider, excludeTypes ...string) []Provider {
	exclude := make(map[string]bool, len(excludeTypes))
	for _, t := range excludeTypes {
		exclude[t] = true
	}
	var result []Provider
	for _, p := range providers {
		if !exclude[p.ProviderType] {
			result = append(result, p)
		}
	}
	return result
}

func parseResponseBody(resp string) (map[string]any, error) {
	parts := strings.SplitN(resp, "\r\n\r\n", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("no body separator found")
	}
	var result map[string]any
	err := json.Unmarshal([]byte(strings.TrimSpace(parts[1])), &result)
	return result, err
}

var _ = ginkgo.Describe("ExternalProvider/ExternalModel E2E", ginkgo.Label("e2e", "new-crds"), func() {
	ginkgo.When("new CRDs with controller-managed networking", ginkgo.Label("tier1"), func() {
		for _, p := range providers {
			p := p

			ginkgo.It(fmt.Sprintf("should return 200 for provider %s", p.ProviderType), ginkgo.Label("smoke"), func() {
				curlCmd := getCurlCommand(p.ModelName)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					if err != nil {
						_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "curl exec error: %v\n", err)
						return false
					}
					return strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200")
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for %s, got:\n%s", p.ProviderType, resp))
			})

			ginkgo.It(fmt.Sprintf("should return OpenAI format response for provider %s", p.ProviderType), func() {
				curlCmd := getCurlCommand(p.ModelName)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && strings.Contains(resp, "200 OK")
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue())

				parts := strings.SplitN(resp, "\r\n\r\n", 2)
				gomega.Expect(len(parts)).To(gomega.Equal(2), "Expected headers and body")

				body := strings.TrimSpace(parts[1])
				var result map[string]any
				err := json.Unmarshal([]byte(body), &result)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Failed to parse JSON: %s", body))

				gomega.Expect(result).To(gomega.HaveKey("choices"))
				gomega.Expect(result).To(gomega.HaveKey("model"))

				choices, ok := result["choices"].([]any)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(len(choices)).To(gomega.BeNumerically(">", 0))
			})
		}
	})

	ginkgo.When("tool calling through BBR pipeline", ginkgo.Label("tier2", "tool-calling"), func() {
		// Tool calling is supported by openai, anthropic, azure-openai, and vertex-openai.
		// bedrock-openai uses the OpenAI path which also supports it.
		for _, p := range providers {
			p := p

			ginkgo.It(fmt.Sprintf("should return tool_calls for provider %s", p.ProviderType), func() {
				curlCmd := getCurlCommandWithTools(p.ModelName)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for tool call on %s, got:\n%s", p.ProviderType, resp))

				result, err := parseResponseBody(resp)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())

				choices, ok := result["choices"].([]any)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(len(choices)).To(gomega.BeNumerically(">", 0))

				choice := choices[0].(map[string]any)
				gomega.Expect(choice["finish_reason"]).To(gomega.Equal("tool_calls"),
					fmt.Sprintf("Expected finish_reason=tool_calls for %s", p.ProviderType))

				msg := choice["message"].(map[string]any)
				toolCalls, ok := msg["tool_calls"].([]any)
				gomega.Expect(ok).To(gomega.BeTrue(), "Expected tool_calls array in message")
				gomega.Expect(len(toolCalls)).To(gomega.BeNumerically(">", 0))

				tc := toolCalls[0].(map[string]any)
				gomega.Expect(tc).To(gomega.HaveKey("id"))
				fn := tc["function"].(map[string]any)
				gomega.Expect(fn["name"]).To(gomega.Equal("get_weather"))
			})
		}
	})

	ginkgo.When("multimodal requests through BBR pipeline", ginkgo.Label("tier2", "multimodal"), func() {
		for _, p := range providers {
			p := p

			ginkgo.It(fmt.Sprintf("should handle image content for provider %s", p.ProviderType), func() {
				curlCmd := getCurlCommandWithImage(p.ModelName)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for multimodal on %s, got:\n%s", p.ProviderType, resp))

				result, err := parseResponseBody(resp)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())

				choices, ok := result["choices"].([]any)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(len(choices)).To(gomega.BeNumerically(">", 0))

				choice := choices[0].(map[string]any)
				msg := choice["message"].(map[string]any)
				content, ok := msg["content"].(string)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(content).To(gomega.ContainSubstring("[image:"),
					fmt.Sprintf("Expected echo response to contain [image: for %s", p.ProviderType))
			})
		}
	})

	ginkgo.When("JSON mode through BBR pipeline", ginkgo.Label("tier2", "json-mode"), func() {
		for _, p := range providers {
			p := p

			ginkgo.It(fmt.Sprintf("should return valid JSON content for provider %s", p.ProviderType), func() {
				curlCmd := getCurlCommandWithJSONMode(p.ModelName)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for JSON mode on %s, got:\n%s", p.ProviderType, resp))

				result, err := parseResponseBody(resp)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())

				choices, ok := result["choices"].([]any)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(len(choices)).To(gomega.BeNumerically(">", 0))

				choice := choices[0].(map[string]any)
				msg := choice["message"].(map[string]any)
				content, ok := msg["content"].(string)
				gomega.Expect(ok).To(gomega.BeTrue())

				var jsonContent map[string]any
				err = json.Unmarshal([]byte(content), &jsonContent)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(),
					fmt.Sprintf("Expected content to be valid JSON for %s, got: %s", p.ProviderType, content))
			})
		}
	})

	ginkgo.When("system prompts and multi-turn", ginkgo.Label("tier2", "conversation"), func() {
		for _, p := range providers {
			p := p

			ginkgo.It(fmt.Sprintf("should handle system prompt for provider %s", p.ProviderType), func() {
				curlCmd := getCurlCommandWithSystemPrompt(p.ModelName)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for system prompt on %s, got:\n%s", p.ProviderType, resp))

				result, err := parseResponseBody(resp)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(result).To(gomega.HaveKey("choices"))
			})

			ginkgo.It(fmt.Sprintf("should handle multi-turn conversation for provider %s", p.ProviderType), func() {
				curlCmd := getCurlCommandMultiTurn(p.ModelName)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for multi-turn on %s, got:\n%s", p.ProviderType, resp))

				result, err := parseResponseBody(resp)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(result).To(gomega.HaveKey("choices"))

				choices := result["choices"].([]any)
				gomega.Expect(len(choices)).To(gomega.BeNumerically(">", 0))
				choice := choices[0].(map[string]any)
				msg := choice["message"].(map[string]any)
				content := msg["content"].(string)
				gomega.Expect(content).To(gomega.ContainSubstring("messages=3"),
					fmt.Sprintf("Echo should show 3 messages for multi-turn on %s", p.ProviderType))
			})
		}
	})

	ginkgo.When("error handling", ginkgo.Label("tier2", "errors"), func() {
		p := providers[0]

		ginkgo.It("should return error for malformed JSON body", func() {
			curlCmd := getCurlCommandRaw(p.ModelName, `{"model": "broken json`)

			var resp string
			gomega.Eventually(func() bool {
				var err error
				resp, err = execInPod("curl", nsName, "curl", curlCmd)
				return err == nil && len(resp) > 0
			}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue())

			gomega.Expect(resp).To(gomega.SatisfyAny(
				gomega.ContainSubstring("400"),
				gomega.ContainSubstring("Bad Request"),
			), fmt.Sprintf("Expected 400 for malformed JSON, got:\n%s", resp))
		})

		ginkgo.It("should return error for empty messages array", func() {
			curlCmd := getCurlCommandRaw(p.ModelName,
				fmt.Sprintf(`{"model":"%s","messages":[]}`, p.ModelName))

			var resp string
			gomega.Eventually(func() bool {
				var err error
				resp, err = execInPod("curl", nsName, "curl", curlCmd)
				return err == nil && len(resp) > 0
			}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue())

			gomega.Expect(resp).To(gomega.SatisfyAny(
				gomega.ContainSubstring("400"),
				gomega.ContainSubstring("Bad Request"),
			), fmt.Sprintf("Expected 400 for empty messages, got:\n%s", resp))
		})

		ginkgo.It("should return 404 for non-existent model path", func() {
			curlCmd := getCurlCommandRaw("nonexistent-model-xyz",
				`{"model":"nonexistent-model-xyz","messages":[{"role":"user","content":"hi"}]}`)

			var resp string
			gomega.Eventually(func() bool {
				var err error
				resp, err = execInPod("curl", nsName, "curl", curlCmd)
				return err == nil && len(resp) > 0
			}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue())

			gomega.Expect(resp).To(gomega.SatisfyAny(
				gomega.ContainSubstring("404"),
				gomega.ContainSubstring("Not Found"),
			), fmt.Sprintf("Expected 404 for non-existent model, got:\n%s", resp))
		})
	})
})
