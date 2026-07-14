package e2e

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// Provider represents a provider configuration for testing.
type Provider struct {
	Name         string
	Provider     string
	SimulatorKey string // expected API key for llm-katan --validate-keys
}

// simulatorKey maps provider names to llm-katan default keys.
// These match the DEFAULT_API_KEYS in llm-katan's config.py.
var simulatorKeys = map[string]string{
	"openai":         "llm-katan-openai-key",
	"anthropic":      "llm-katan-anthropic-key",
	"azure-openai":   "llm-katan-azure-key",
	"vertex":         "llm-katan-vertexai-key",
	"bedrock-openai": "llm-katan-bedrock-key",
}

var providers = []Provider{
	{Name: "e2e-openai", Provider: "openai", SimulatorKey: simulatorKeys["openai"]},
	{Name: "e2e-anthropic", Provider: "anthropic", SimulatorKey: simulatorKeys["anthropic"]},
	{Name: "e2e-azure", Provider: "azure-openai", SimulatorKey: simulatorKeys["azure-openai"]},
	// vertex (native GenerateContent) commented out — not used in 3.4 ExternalModel flow.
	// {Name: "e2e-vertex", Provider: "vertex", SimulatorKey: simulatorKeys["vertex"]},
	// bedrock-openai uses /v1/chat/completions (same as OpenAI), so the simulator
	// validates against the OpenAI key until key-based provider dispatch is implemented.
	{Name: "e2e-bedrock", Provider: "bedrock-openai", SimulatorKey: simulatorKeys["openai"]},
	{Name: "e2e-vertex-openai", Provider: "vertex-openai", SimulatorKey: simulatorKeys["vertex"]},
}

func createProviderResources(p Provider) {
	// Create Secret based on provider auth requirements
	if p.Provider == "vertex-openai" {
		// vertex-openai uses GCP OAuth2 - provide mock service account JSON
		// with token_uri pointing to the mock token server deployed in the test namespace
		mockTokenServerURL := fmt.Sprintf("http://mock-token-server.%s.svc.cluster.local:8081/token", nsName)
		serviceAccountJSON := createMockServiceAccountJSON(mockTokenServerURL)
		kubectlApplyLiteral(fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  gcp-service-account-json: '%s'
`, p.Name, nsName, serviceAccountJSON))
	} else {
		// Other providers use simple API key
		kubectlApplyLiteral(fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    inference.llm-d.ai/ipp-managed: "true"
type: Opaque
stringData:
  api-key: %s
`, p.Name, nsName, p.SimulatorKey))
	}

	// ExternalProvider CR - set auth.type based on provider for test setup
	authType := "apikey"
	if p.Provider == "vertex-openai" {
		authType = "oauth2"
	}
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
    type: %s
    secretRef:
      name: %s
`, p.Name, nsName, p.Provider, simulatorFQDN, authType, p.Name))

	// ExternalModel CR
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
      apiFormat: %s
      path: %s
`, p.Name, nsName, p.Name, p.Name, apiFormatForProvider(p.Provider), defaultPathForProvider(p.Provider)))
}

func apiFormatForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "messages"
	default:
		return "openai-chat"
	}
}

func defaultPathForProvider(provider string) string {
	switch provider {
	case "anthropic":
		return "/v1/messages"
	case "azure-openai":
		return "/openai/v1/chat/completions"
	case "vertex-openai":
		return "/v1/projects/test-project/locations/us-central1/endpoints/openapi/chat/completions"
	default:
		return "/v1/chat/completions"
	}
}

func deleteProviderResources(p Provider) {
	// Controller-created resources (Service, ServiceEntry, DestinationRule, HTTPRoute)
	// are cleaned up via owner references when the CRs are deleted.
	kubectlDeleteResource("externalmodels.inference.opendatahub.io", p.Name, nsName)
	kubectlDeleteResource("externalproviders.inference.opendatahub.io", p.Name, nsName)
	kubectlDeleteResource("secret", p.Name, nsName)
}

func buildCurlCommand(modelName string, body map[string]any) []string {
	bodyBytes, _ := json.Marshal(body)

	gatewayURL := fmt.Sprintf("http://%s.%s.svc:80/%s/%s/v1/chat/completions",
		gatewaySvcName, gatewayNs, nsName, modelName)

	return []string{
		"curl", "-si", "--max-time", strconv.Itoa(int(curlTimeout.Seconds())),
		gatewayURL,
		"-H", "Content-Type: application/json",
		"-H", "Connection: close",
		"-d", string(bodyBytes),
	}
}

// getCurlCommand builds a curl command to send a chat completion request
// from inside the cluster to the gateway service.
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
		"tools": []map[string]any{{
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
		}},
	})
}

func getCurlCommandWithImage(modelName string) []string {
	return buildCurlCommand(modelName, map[string]any{
		"model": modelName,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "What is this?"},
				{"type": "image_url", "image_url": map[string]string{
					"url": "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==",
				}},
			},
		}},
	})
}

func getCurlCommandWithJSONMode(modelName string) []string {
	return buildCurlCommand(modelName, map[string]any{
		"model":           modelName,
		"messages":        []map[string]string{{"role": "user", "content": "list 3 colors as JSON"}},
		"response_format": map[string]string{"type": "json_object"},
	})
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

var _ = ginkgo.Describe("IPP Plugin Chain", ginkgo.Label("e2e"), func() {
	ginkgo.When("IPP is deployed with all plugins", ginkgo.Label("tier1"), func() {
		for _, p := range providers {
			p := p // capture range variable

			ginkgo.It(fmt.Sprintf("should return 200 for provider %s", p.Provider), ginkgo.Label("smoke", "sanity"), func() {
				curlCmd := getCurlCommand(p.Name)

				var resp string
				var err error

				// Retry with backoff (route propagation may take time)
				gomega.Eventually(func() bool {
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					if err != nil {
						_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "curl exec error: %v\n", err)
						return false
					}
					return strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200")
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for %s, got:\n%s", p.Provider, resp))
			})

			ginkgo.It(fmt.Sprintf("should return OpenAI format response for provider %s", p.Provider), func() {
				curlCmd := getCurlCommand(p.Name)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && strings.Contains(resp, "200 OK")
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue())

				// Extract JSON body from response (after empty line)
				parts := strings.SplitN(resp, "\r\n\r\n", 2)
				gomega.Expect(len(parts)).To(gomega.Equal(2), "Expected headers and body separated by empty line")

				body := strings.TrimSpace(parts[1])
				var result map[string]any
				err := json.Unmarshal([]byte(body), &result)
				gomega.Expect(err).NotTo(gomega.HaveOccurred(), fmt.Sprintf("Failed to parse JSON: %s", body))

				gomega.Expect(result).To(gomega.HaveKey("choices"), fmt.Sprintf("Missing 'choices': %v", result))
				gomega.Expect(result).To(gomega.HaveKey("model"), fmt.Sprintf("Missing 'model': %v", result))

				choices, ok := result["choices"].([]any)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(len(choices)).To(gomega.BeNumerically(">", 0))
			})
		}
	})

	// Test that an invalid API key is rejected by the simulator when --validate-keys is enabled.
	// Only runs when E2E_SIMULATOR_VALIDATE_KEYS=true (simulator must be started with --validate-keys).
	ginkgo.When("simulator has key validation enabled", ginkgo.Label("tier2"), func() {
		ginkgo.BeforeEach(func() {
			if os.Getenv("E2E_SIMULATOR_VALIDATE_KEYS") != "true" {
				ginkgo.Skip("E2E_SIMULATOR_VALIDATE_KEYS not set, skipping key validation test")
			}
		})

		ginkgo.It("should reject requests with an invalid API key", func() {
			wrongKeyProvider := Provider{
				Name:         "e2e-wrong-key",
				Provider:     "openai",
				SimulatorKey: "intentionally-wrong-key",
			}
			createProviderResources(wrongKeyProvider)
			defer deleteProviderResources(wrongKeyProvider)

			time.Sleep(5 * time.Second)

			curlCmd := getCurlCommand(wrongKeyProvider.Name)
			var resp string

			gomega.Eventually(func() bool {
				var err error
				resp, err = execInPod("curl", nsName, "curl", curlCmd)
				if err != nil {
					return false
				}
				return strings.Contains(resp, "401")
			}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
				fmt.Sprintf("Expected 401 for wrong key, got:\n%s", resp))

			// Verify the error message contains the expected key hint
			gomega.Expect(resp).To(gomega.ContainSubstring("expected"),
				"401 response should include the expected key in the error message")
		})
	})

	ginkgo.When("tool calling through BBR pipeline", ginkgo.Label("tier2", "tool-calling"), func() {
		for _, p := range providers {
			p := p

			ginkgo.It(fmt.Sprintf("should return tool_calls for provider %s", p.Provider), func() {
				curlCmd := getCurlCommandWithTools(p.Name)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for tool call on %s, got:\n%s", p.Provider, resp))

				result, err := parseResponseBody(resp)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())

				choices, ok := result["choices"].([]any)
				gomega.Expect(ok).To(gomega.BeTrue())
				gomega.Expect(len(choices)).To(gomega.BeNumerically(">", 0))

				choice := choices[0].(map[string]any)
				gomega.Expect(choice["finish_reason"]).To(gomega.Equal("tool_calls"),
					fmt.Sprintf("Expected finish_reason=tool_calls for %s", p.Provider))

				msg := choice["message"].(map[string]any)
				toolCalls, ok := msg["tool_calls"].([]any)
				gomega.Expect(ok).To(gomega.BeTrue(), "Expected tool_calls array")
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

			ginkgo.It(fmt.Sprintf("should handle image content for provider %s", p.Provider), func() {
				curlCmd := getCurlCommandWithImage(p.Name)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for multimodal on %s, got:\n%s", p.Provider, resp))

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
					fmt.Sprintf("Expected echo response to contain [image: for %s", p.Provider))
			})
		}
	})

	ginkgo.When("JSON mode through BBR pipeline", ginkgo.Label("tier2", "json-mode"), func() {
		for _, p := range providers {
			p := p

			ginkgo.It(fmt.Sprintf("should return valid JSON content for provider %s", p.Provider), func() {
				curlCmd := getCurlCommandWithJSONMode(p.Name)

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for JSON mode on %s, got:\n%s", p.Provider, resp))

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
					fmt.Sprintf("Expected valid JSON content for %s, got: %s", p.Provider, content))
			})
		}
	})

	ginkgo.When("system prompts and multi-turn conversations", ginkgo.Label("tier2", "conversation"), func() {
		for _, p := range providers {
			p := p

			ginkgo.It(fmt.Sprintf("should handle system prompt for provider %s", p.Provider), func() {
				curlCmd := buildCurlCommand(p.Name, map[string]any{
					"model": p.Name,
					"messages": []map[string]string{
						{"role": "system", "content": "You are a helpful assistant."},
						{"role": "user", "content": "hello"},
					},
				})

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for system prompt on %s, got:\n%s", p.Provider, resp))

				result, err := parseResponseBody(resp)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(result).To(gomega.HaveKey("choices"))
			})

			ginkgo.It(fmt.Sprintf("should handle multi-turn conversation for provider %s", p.Provider), func() {
				curlCmd := buildCurlCommand(p.Name, map[string]any{
					"model": p.Name,
					"messages": []map[string]string{
						{"role": "user", "content": "my name is test-user"},
						{"role": "assistant", "content": "nice to meet you"},
						{"role": "user", "content": "what is my name"},
					},
				})

				var resp string
				gomega.Eventually(func() bool {
					var err error
					resp, err = execInPod("curl", nsName, "curl", curlCmd)
					return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
				}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
					fmt.Sprintf("Expected 200 for multi-turn on %s, got:\n%s", p.Provider, resp))

				result, err := parseResponseBody(resp)
				gomega.Expect(err).NotTo(gomega.HaveOccurred())
				gomega.Expect(result).To(gomega.HaveKey("choices"))
			})
		}
	})
})

// createMockServiceAccountJSON creates a fake GCP service account JSON for E2E testing.
// The token_uri points to the mock token server which returns a static token.
func createMockServiceAccountJSON(tokenURL string) string {
	// This is a minimal service account JSON structure that Google's library can parse.
	// The private key is a valid RSA key format but not a real GCP key.
	sa := map[string]any{
		"type":                        "service_account",
		"project_id":                  "e2e-test-project",
		"private_key_id":              "e2e-key-id",
		"private_key":                 generateTestPrivateKeyPEM(),
		"client_email":                "e2e-test@e2e-test-project.iam.gserviceaccount.com",
		"client_id":                   "123456789",
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   tokenURL,
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url":        "https://www.googleapis.com/robot/v1/metadata/x509/e2e-test",
	}
	jsonBytes, _ := json.Marshal(sa)
	return string(jsonBytes)
}

// generateTestPrivateKeyPEM generates a fresh RSA private key for testing.
func generateTestPrivateKeyPEM() string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("failed to generate RSA key: %v", err))
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal private key: %v", err))
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}
