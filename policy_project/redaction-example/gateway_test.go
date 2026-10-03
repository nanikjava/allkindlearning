package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testKey = "test-signing-key"

var bundlePath string

// TestMain compiles the approved policy with policyc, exactly as the
// publish step would, and the tests run against that signed bundle.
func TestMain(m *testing.M) {
	dir, _ := os.MkdirTemp("", "policyc")
	cmd := exec.Command("go", "run", "./cmd/policyc", "-out", dir)
	cmd.Env = append(os.Environ(), "POLICY_SIGNING_KEY="+testKey)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		panic("policyc failed: " + err.Error())
	}
	bundlePath = filepath.Join(dir, "pol_ai_003-2.1.tar.gz")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// fakeModel records what actually reached the "provider".
func fakeModel(t *testing.T, got *string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*got = string(b)
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
}

func newGateway(t *testing.T, upstream string, logs *bytes.Buffer) *Gateway {
	p, err := LoadBundle(context.Background(), bundlePath, testKey)
	if err != nil {
		t.Fatal(err)
	}
	ext := Destination{Provider: "openai", Hosting: "external"}
	return &Gateway{
		Policy: p, CheckTimeout: 100 * time.Millisecond, Client: http.DefaultClient,
		Log: slog.New(slog.NewJSONHandler(logs, nil)),
		Keys: map[string]Key{
			"gw-sales":    {App: App{ID: "sales-copilot"}, Destination: ext, UpstreamURL: upstream, UpstreamKey: "real"},
			"gw-support":  {App: App{ID: "support-bot"}, Destination: ext, UpstreamURL: upstream, UpstreamKey: "real"},
			"gw-hr":       {App: App{ID: "hr-assistant", ApprovedFor: []string{"personal-data"}}, Destination: ext, UpstreamURL: upstream, UpstreamKey: "real"},
			"gw-claims":   {App: App{ID: "claims-assistant"}, Destination: ext, UpstreamURL: upstream, UpstreamKey: "real"},
			"gw-selfhost": {App: App{ID: "sales-copilot"}, Destination: Destination{Provider: "vllm", Hosting: "self-hosted"}, UpstreamURL: upstream, UpstreamKey: "real"},
		},
	}
}

func send(g *Gateway, key, content string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{
		"model":       "gpt-4o",
		"temperature": 0.2,
		"messages":    []Message{{Role: "user", Content: content}},
	})
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

const prompt = "Customer jane.doe@example.com (phone +1 415-555-0134) says her refund is late. Jane's backup email is jane.doe@example.com."

func TestRedactsPIIForExternalModel(t *testing.T) {
	var got string
	var logs bytes.Buffer
	up := fakeModel(t, &got)
	defer up.Close()
	w := send(newGateway(t, up.URL, &logs), "gw-sales", prompt)

	if w.Code != 200 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if strings.Contains(got, "jane.doe@example.com") || strings.Contains(got, "555-0134") {
		t.Fatalf("PII reached the model: %s", got)
	}
	if !strings.Contains(got, "[EMAIL_1]") || !strings.Contains(got, "[PHONE_1]") {
		t.Fatalf("placeholders missing: %s", got)
	}
	if strings.Contains(got, "[EMAIL_2]") {
		t.Fatalf("same email should reuse one placeholder: %s", got)
	}
	if !strings.Contains(got, `"temperature":0.2`) {
		t.Fatalf("other request fields must pass through: %s", got)
	}
	if strings.Contains(logs.String(), "jane.doe") {
		t.Fatalf("audit log must not contain the data itself: %s", logs.String())
	}
	t.Logf("sent to model: %s", got)
	t.Logf("audit log:     %s", strings.TrimSpace(logs.String()))
}

// support-bot is rolled out in monitor mode: the decision is logged as
// evidence but the request goes through unchanged.
func TestMonitorModeLogsButDoesNotRedact(t *testing.T) {
	var got string
	var logs bytes.Buffer
	up := fakeModel(t, &got)
	defer up.Close()
	send(newGateway(t, up.URL, &logs), "gw-support", prompt)
	if !strings.Contains(got, "jane.doe@example.com") {
		t.Fatalf("monitor mode must not change the request: %s", got)
	}
	if !strings.Contains(logs.String(), `"mode":"monitor"`) {
		t.Fatalf("monitor decision must be logged: %s", logs.String())
	}
}

func TestTamperedBundleIsRejected(t *testing.T) {
	if _, err := LoadBundle(context.Background(), bundlePath, "wrong-key"); err == nil {
		t.Fatal("bundle signed with another key must not load")
	}
}

func TestApprovedAppIsNotRedacted(t *testing.T) {
	var got string
	up := fakeModel(t, &got)
	defer up.Close()
	send(newGateway(t, up.URL, &bytes.Buffer{}), "gw-hr", prompt)
	if !strings.Contains(got, "jane.doe@example.com") {
		t.Fatalf("approved app should send data unchanged: %s", got)
	}
}

func TestActiveExceptionSkipsRedaction(t *testing.T) {
	var got string
	up := fakeModel(t, &got)
	defer up.Close()
	send(newGateway(t, up.URL, &bytes.Buffer{}), "gw-claims", prompt)
	if !strings.Contains(got, "jane.doe@example.com") {
		t.Fatalf("app with active exception should not be redacted: %s", got)
	}
}

func TestSelfHostedModelIsNotRedacted(t *testing.T) {
	var got string
	up := fakeModel(t, &got)
	defer up.Close()
	send(newGateway(t, up.URL, &bytes.Buffer{}), "gw-selfhost", prompt)
	if !strings.Contains(got, "jane.doe@example.com") {
		t.Fatalf("self-hosted destination is out of scope for 3.1: %s", got)
	}
}

func TestCardNumberIsBlocked(t *testing.T) {
	var got string
	up := fakeModel(t, &got)
	defer up.Close()
	w := send(newGateway(t, up.URL, &bytes.Buffer{}), "gw-hr", "Charge card 4111 1111 1111 1111 for the order")
	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", w.Code)
	}
	if got != "" {
		t.Fatalf("blocked request must not reach the model")
	}
	t.Logf("response: %s", strings.TrimSpace(w.Body.String()))
}

func TestRestore(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "Email jane@example.com now"}}
	fs := Detect(msgs)
	vault, _ := Redact(msgs, fs, []string{fs[0].ID})
	reply := "I've drafted a note to [EMAIL_1]."
	if Restore(reply, vault) != "I've drafted a note to jane@example.com." {
		t.Fatalf("restore failed: %s", Restore(reply, vault))
	}
}
