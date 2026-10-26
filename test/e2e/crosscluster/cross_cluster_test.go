package crosscluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
)

// received mirrors the JSON the echo backend puts in the assistant message.
type received struct {
	Path    string            `json:"path"`
	Model   string            `json:"model"`
	Headers map[string]string `json:"headers"`
}

var _ = ginkgo.Describe("Cross-cluster routing", ginkgo.Label("e2e", "cross-cluster"), func() {

	ginkgo.It("generates TLS-originating Istio resources for the remote-maas provider", func() {
		mode, err := kubectlOutput(ctxA, "get", "destinationrule", "cluster-b", "-n", nsA,
			"-o", "jsonpath={.spec.trafficPolicy.tls.mode}")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), mode)
		gomega.Expect(mode).To(gomega.Equal("SIMPLE"))

		port, err := kubectlOutput(ctxA, "get", "serviceentry", "cluster-b", "-n", nsA,
			"-o", "jsonpath={.spec.ports[0].number}")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), port)
		gomega.Expect(port).To(gomega.Equal(gatewayBPort))

		host, err := kubectlOutput(ctxA, "get", "serviceentry", "cluster-b", "-n", nsA,
			"-o", "jsonpath={.spec.hosts[0]}")
		gomega.Expect(err).NotTo(gomega.HaveOccurred(), host)
		gomega.Expect(host).To(gomega.Equal(gatewayBHost))
	})

	ginkgo.It("forwards a request through cluster B to the backend with the configured path and credential", func() {
		var got received
		// The first request can race Envoy picking up freshly created routes.
		gomega.Eventually(func() int {
			status, body := postChat(modelRemoteEcho, nil)
			if status == http.StatusOK {
				got = parseReceived(body)
			}
			return status
		}, 90*time.Second, 3*time.Second).Should(gomega.Equal(http.StatusOK))

		gomega.Expect(got.Path).To(gomega.Equal("/v1/chat/completions"), "path override from ExternalModel.path")
		gomega.Expect(got.Model).To(gomega.Equal(modelEchoTarget), "model rewritten to targetModel at each hop")
		gomega.Expect(got.Headers["authorization"]).To(gomega.Equal("Bearer "+keyEchoBackend),
			"cluster B replaces the cross-cluster credential with the backend provider credential")
		gomega.Expect(got.Headers).NotTo(gomega.HaveKey("x-origin-cluster"),
			"internal origin header must not leak to a third-party upstream")
	})

	ginkgo.It("preserves B3 trace headers across the cross-cluster hop", func() {
		const traceID = "463ac35c9f6413ad48485a3953bb6124"
		status, body := postChat(modelRemoteEcho, map[string]string{
			"x-b3-traceid": traceID,
			"x-b3-spanid":  "a2fb4a1d1a96d312",
			"x-b3-sampled": "1",
		})
		gomega.Expect(status).To(gomega.Equal(http.StatusOK), string(body))
		got := parseReceived(body)
		gomega.Expect(got.Headers["x-b3-traceid"]).To(gomega.Equal(traceID))
		gomega.Expect(got.Headers["x-request-id"]).NotTo(gomega.BeEmpty())
	})

	ginkgo.It("rejects an A→B→A routing loop with 403", func() {
		status, body := postChat(modelLoopOrigin, nil)
		gomega.Expect(status).To(gomega.Equal(http.StatusForbidden), string(body))
		gomega.Expect(string(body)).To(gomega.ContainSubstring("routing loop detected"))
	})
})

func postChat(model string, headers map[string]string) (int, []byte) {
	payload, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
	})
	req, err := http.NewRequest(http.MethodPost, gatewayAURL+"/v1/chat/completions", bytes.NewReader(payload))
	gomega.Expect(err).NotTo(gomega.HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer user-key-from-client")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "request failed: %v\n", err)
		return 0, nil
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	_, _ = fmt.Fprintf(ginkgo.GinkgoWriter, "POST model=%s -> %d %s\n", model, resp.StatusCode, body)
	return resp.StatusCode, body
}

func parseReceived(body []byte) received {
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	gomega.Expect(json.Unmarshal(body, &completion)).To(gomega.Succeed(), string(body))
	gomega.Expect(completion.Choices).NotTo(gomega.BeEmpty(), string(body))
	var got received
	gomega.Expect(json.Unmarshal([]byte(completion.Choices[0].Message.Content), &got)).To(gomega.Succeed(),
		completion.Choices[0].Message.Content)
	return got
}
