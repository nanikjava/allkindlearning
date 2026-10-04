package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// PIIService calls the NER sidecar in pii-service/ (spaCy/Presidio on :3002
// or GLiNER2 on :3001). Both expose the same POST /detect API and return
// UTF-8 byte offsets, which match Go string indexes.
type PIIService struct {
	URL    string // e.g. http://127.0.0.1:3002
	Client *http.Client
}

type serviceFinding struct {
	Type      string  `json:"type"`
	Score     float64 `json:"score"`
	ByteStart int     `json:"byte_start"`
	ByteEnd   int     `json:"byte_end"`
}

// Detect sends one message's text to the sidecar and returns its findings
// as gateway findings. IDs are left empty for the caller to assign.
func (s *PIIService) Detect(ctx context.Context, msg int, text string) ([]Finding, error) {
	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.URL, "/")+"/detect", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pii service returned %s", resp.Status)
	}
	var out struct {
		Findings []serviceFinding `json:"findings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode pii service response: %w", err)
	}
	var fs []Finding
	for _, f := range out.Findings {
		// Never trust offsets from another process to be in range.
		if f.ByteStart < 0 || f.ByteEnd > len(text) || f.ByteStart >= f.ByteEnd {
			return nil, fmt.Errorf("pii service returned out-of-range span %d-%d", f.ByteStart, f.ByteEnd)
		}
		fs = append(fs, Finding{Type: f.Type, Message: msg, Start: f.ByteStart, End: f.ByteEnd})
	}
	return fs, nil
}

// DetectAll runs the regex detectors and, if svc is set, the PII service,
// then merges the results. Regex findings win on overlap because they are
// exact matches (checksums, fixed formats); the model adds what regex cannot
// see, such as names and addresses.
func DetectAll(ctx context.Context, svc *PIIService, msgs []Message) ([]Finding, error) {
	findings := Detect(msgs)
	if svc == nil {
		return findings, nil
	}
	n := len(findings)
	for mi, m := range msgs {
		if m.Content == "" {
			continue
		}
		extra, err := svc.Detect(ctx, mi, m.Content)
		if err != nil {
			return nil, err
		}
		for _, f := range extra {
			if overlapsAny(findings, f) {
				continue
			}
			n++
			f.ID = fmt.Sprintf("f%d", n)
			findings = append(findings, f)
		}
	}
	return findings, nil
}

func overlapsAny(fs []Finding, f Finding) bool {
	for _, g := range fs {
		if g.Message == f.Message && f.Start < g.End && g.Start < f.End {
			return true
		}
	}
	return false
}
