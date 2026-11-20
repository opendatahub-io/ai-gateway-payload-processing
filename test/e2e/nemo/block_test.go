package nemo

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// RHOAIENG-99423: a NeMo input block through the gateway must return promptly
// with HTTP 403 and a non-empty JSON body, for both streaming and non-streaming
// chat completions. The request is sent from inside the cluster to the gateway
// service, which is the same path a client uses.

func TestNeMoInputBlockViaGateway(t *testing.T) {
	ns := envOr("E2E_GATEWAY_NAMESPACE", "openshift-ingress")
	svc := envOr("E2E_GATEWAY_SVC_NAME", "maas-default-gateway-istio")
	model := envOr("E2E_MODEL_NAME", "gpt-4o")
	ensureCurlPod(t, ns)

	cases := []struct {
		name   string
		stream bool
	}{
		{name: "non-streaming", stream: false},
		{name: "streaming", stream: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{
				"model": model,
				"messages": []map[string]string{{
					"role":    "user",
					"content": "My email is john@example.com",
				}},
				"max_tokens": 10,
			}
			if tc.stream {
				body["stream"] = true
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			url := fmt.Sprintf("http://%s.%s.svc/v1/chat/completions", svc, ns)
			start := time.Now()
			out, err := kubectl(nil, "exec", "-n", ns, "nemo-e2e-curl", "-c", "curl", "--",
				"curl", "-si", "--max-time", "20", url,
				"-H", "Content-Type: application/json",
				"-H", "Connection: close",
				"-d", string(raw),
			)
			elapsed := time.Since(start)
			if err != nil {
				t.Fatalf("gateway request failed after %s: %v\n%s", elapsed, err, out)
			}
			if elapsed > 20*time.Second {
				t.Fatalf("blocked request took %s, want it to finish before the route timeout", elapsed)
			}
			status, respBody := splitHTTP(out)
			if !strings.Contains(status, "403") {
				t.Fatalf("status %q, want 403\n%s", status, out)
			}
			if strings.TrimSpace(respBody) == "" {
				t.Fatal("403 body is empty")
			}
			var parsed struct {
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
				t.Fatalf("403 body is not JSON: %v\n%s", err, respBody)
			}
			if parsed.Error.Message == "" {
				t.Fatalf("403 JSON has an empty error.message: %s", respBody)
			}
		})
	}
}

func ensureCurlPod(t *testing.T, ns string) {
	t.Helper()
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: nemo-e2e-curl
  namespace: %s
spec:
  containers:
  - name: curl
    image: curlimages/curl:8.10.1
    command: ["sleep", "3600"]
`, ns)
	if out, err := kubectl(strings.NewReader(manifest), "apply", "-f", "-"); err != nil {
		t.Fatalf("create curl pod: %v\n%s", err, out)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		out, err := kubectl(nil, "get", "pod", "nemo-e2e-curl", "-n", ns, "-o", "jsonpath={.status.phase}")
		if err == nil && strings.TrimSpace(out) == "Running" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	out, _ := kubectl(nil, "describe", "pod", "nemo-e2e-curl", "-n", ns)
	t.Fatalf("curl pod not running:\n%s", out)
}

func splitHTTP(raw string) (status, body string) {
	parts := strings.SplitN(raw, "\r\n\r\n", 2)
	if len(parts) == 1 {
		parts = strings.SplitN(raw, "\n\n", 2)
	}
	status = parts[0]
	if len(parts) == 2 {
		body = strings.TrimSpace(parts[1])
	}
	return status, body
}

func kubectl(stdin *strings.Reader, args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
