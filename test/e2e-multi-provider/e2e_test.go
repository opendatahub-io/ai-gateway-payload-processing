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

package e2e_multi_provider

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

func getCurlCommand(modelName string) []string {
	body := map[string]any{
		"model":    modelName,
		"messages": []map[string]string{{"role": "user", "content": "hello from " + modelName}},
	}
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

func parseResponseBody(resp string) (map[string]any, error) {
	parts := strings.SplitN(resp, "\r\n\r\n", 2)
	if len(parts) < 2 {
		return nil, fmt.Errorf("no body separator found")
	}
	var result map[string]any
	err := json.Unmarshal([]byte(strings.TrimSpace(parts[1])), &result)
	return result, err
}

var _ = ginkgo.Describe("Multi-Provider Traffic Splitting", ginkgo.Label("e2e", "multi-provider"), func() {
	ginkgo.When("model has 80/20 weighted providers (openai/anthropic)", ginkgo.Label("tier2"), func() {

		ginkgo.It("should return 200 for all requests", func() {
			curlCmd := getCurlCommand("multi-split")

			var resp string
			gomega.Eventually(func() bool {
				var err error
				resp, err = execInPod("curl", nsName, "curl", curlCmd)
				return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
			}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue(),
				fmt.Sprintf("Expected 200 for multi-split, got:\n%s", resp))
		})

		ginkgo.It("should return valid OpenAI format from both providers", func() {
			curlCmd := getCurlCommand("multi-split")

			var resp string
			gomega.Eventually(func() bool {
				var err error
				resp, err = execInPod("curl", nsName, "curl", curlCmd)
				return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
			}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue())

			result, err := parseResponseBody(resp)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			gomega.Expect(result).To(gomega.HaveKey("choices"))
			gomega.Expect(result).To(gomega.HaveKey("model"))
		})

		ginkgo.It("should split traffic across both providers", func() {
			openaiCount := 0
			anthropicCount := 0
			totalRequests := 50

			for i := 0; i < totalRequests; i++ {
				curlCmd := getCurlCommand("multi-split")
				resp, err := execInPod("curl", nsName, "curl", curlCmd)
				if err != nil || !strings.Contains(resp, "200") {
					continue
				}

				result, err := parseResponseBody(resp)
				if err != nil {
					continue
				}

				id, _ := result["id"].(string)
				if strings.HasPrefix(id, "chatcmpl") {
					openaiCount++
				} else if strings.HasPrefix(id, "msg_") {
					anthropicCount++
				}
			}

			total := openaiCount + anthropicCount
			_, _ = fmt.Fprintf(ginkgo.GinkgoWriter,
				"Traffic distribution: OpenAI=%d Anthropic=%d Total=%d\n",
				openaiCount, anthropicCount, total)

			gomega.Expect(total).To(gomega.BeNumerically(">=", 40),
				"At least 40 of 50 requests should succeed")
			gomega.Expect(openaiCount).To(gomega.BeNumerically(">", 0),
				"OpenAI provider should receive some traffic")
			gomega.Expect(anthropicCount).To(gomega.BeNumerically(">", 0),
				"Anthropic provider should receive some traffic")
			gomega.Expect(openaiCount).To(gomega.BeNumerically(">", anthropicCount),
				"OpenAI (weight=80) should get more traffic than Anthropic (weight=20)")
		})

		ginkgo.It("should rewrite model field to targetModel", func() {
			curlCmd := getCurlCommand("multi-split")

			var resp string
			gomega.Eventually(func() bool {
				var err error
				resp, err = execInPod("curl", nsName, "curl", curlCmd)
				return err == nil && (strings.Contains(resp, "200 OK") || strings.Contains(resp, "HTTP/1.1 200"))
			}, curlTimeout*3, 5*time.Second).Should(gomega.BeTrue())

			result, err := parseResponseBody(resp)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())

			// The echo backend should show the targetModel (llm-katan-echo),
			// not the client-facing model name (multi-split)
			model, ok := result["model"].(string)
			gomega.Expect(ok).To(gomega.BeTrue())
			gomega.Expect(model).To(gomega.Equal("llm-katan-echo"),
				"Response model should be the targetModel, not the client-facing name")
		})
	})
})
