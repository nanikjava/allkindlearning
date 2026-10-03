package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// Message is the part of an OpenAI-style chat message the gateway inspects.
// Other fields are passed through untouched via Raw.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Key is what the gateway knows about a virtual key it issued to a team.
type Key struct {
	App         App
	Destination Destination
	UpstreamURL string // e.g. https://api.openai.com
	UpstreamKey string // the real provider key; the team never sees it
}

type Gateway struct {
	Keys         map[string]Key // virtual key -> app
	Policy       *Policy
	CheckTimeout time.Duration
	Client       *http.Client
	Log          *slog.Logger
}

// ServeHTTP handles POST /v1/chat/completions. Teams adopt it by setting
// OPENAI_BASE_URL to the gateway and using their gateway-issued key.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key, ok := g.Keys[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unknown gateway key", nil)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "cannot read body", nil)
		return
	}
	// Decode into a generic map so unknown fields (tools, temperature, ...)
	// survive, and decode messages separately so we can edit them.
	var raw map[string]json.RawMessage
	var req struct {
		Messages []Message `json:"messages"`
	}
	if json.Unmarshal(body, &raw) != nil || json.Unmarshal(body, &req) != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON", nil)
		return
	}

	// 1. Detect sensitive data.
	findings := Detect(req.Messages)

	// 2. Ask the policy what to do. Both statements in this policy are
	//    configured to fail closed, so a policy error blocks the request.
	ctx, cancel := context.WithTimeout(r.Context(), g.CheckTimeout)
	defer cancel()
	res, err := g.Policy.Eval(ctx, PolicyInput{App: key.App, Destination: key.Destination, Findings: findings})
	if err != nil {
		g.Log.Error("policy check failed; failing closed", "app", key.App.ID, "err", err)
		writeErr(w, http.StatusServiceUnavailable, "policy check unavailable", nil)
		return
	}

	// 3. Apply decisions. Monitor-mode decisions are only logged, so owners
	//    can see the impact of a new rule before it changes any traffic.
	//    Block wins over redact.
	var enforce []Decision
	for _, d := range res.Decisions {
		if d.Mode == "monitor" {
			g.audit(key, res, d, findings, 0)
			continue
		}
		enforce = append(enforce, d)
	}
	for _, d := range enforce {
		if d.Action == "block" {
			g.audit(key, res, d, findings, 0)
			writeErr(w, http.StatusForbidden,
				"request blocked by "+res.PolicyID+" statement "+d.Statement, &d)
			return
		}
	}
	for _, d := range enforce {
		if d.Action == "redact" {
			_, n := Redact(req.Messages, findings, d.FindingIDs)
			g.audit(key, res, d, findings, n)
		}
	}

	// 4. Forward the (possibly rewritten) request with the real provider key.
	raw["messages"], _ = json.Marshal(req.Messages)
	out, _ := json.Marshal(raw)
	up, _ := http.NewRequestWithContext(r.Context(), http.MethodPost,
		key.UpstreamURL+"/v1/chat/completions", bytes.NewReader(out))
	up.Header.Set("Content-Type", "application/json")
	up.Header.Set("Authorization", "Bearer "+key.UpstreamKey)
	resp, err := g.Client.Do(up)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "upstream unavailable", nil)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// audit writes the evidence record. It holds types and counts only, never
// the sensitive values themselves.
func (g *Gateway) audit(k Key, res *Result, d Decision, fs []Finding, redacted int) {
	types := map[string]int{}
	want := map[string]bool{}
	for _, id := range d.FindingIDs {
		want[id] = true
	}
	for _, f := range fs {
		if want[f.ID] {
			types[f.Type]++
		}
	}
	g.Log.Info("policy_decision",
		"app", k.App.ID,
		"destination", k.Destination.Provider,
		"policy", res.PolicyID,
		"policy_version", res.PolicyVersion,
		"statement", d.Statement,
		"action", d.Action,
		"mode", d.Mode,
		"bundle_revision", g.Policy.Revision,
		"finding_types", types,
		"redacted", redacted,
	)
}

func writeErr(w http.ResponseWriter, code int, msg string, d *Decision) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body := map[string]any{"error": map[string]any{"message": msg, "type": "policy_violation"}}
	if d != nil {
		body["error"].(map[string]any)["statement"] = d.Statement
	}
	json.NewEncoder(w).Encode(body)
}
