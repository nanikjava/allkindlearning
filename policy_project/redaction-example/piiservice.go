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
	Type      string  `json:"type"`  // gateway finding type, e.g. "pii.name"
	Label     string  `json:"label"` // engine's entity type, e.g. "PERSON"
	Score     float64 `json:"score"`
	Start     int     `json:"start"` // character (rune) offsets
	End       int     `json:"end"`
	ByteStart int     `json:"byte_start"` // UTF-8 byte offsets, match Go string indexes
	ByteEnd   int     `json:"byte_end"`
}

// RedactResult is the sidecar's POST /redact response: the text with each
// finding replaced by a placeholder like [NAME_1], plus the findings. The
// placeholder-to-original vault stays in the sidecar and is not returned.
type RedactResult struct {
	Text     string           `json:"text"`
	Findings []serviceFinding `json:"findings"`
}

// post sends {"text": text} to the sidecar path and decodes the JSON reply into out.
func (s *PIIService) post(ctx context.Context, path, text string, out any) error {
	body, _ := json.Marshal(map[string]string{"text": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("pii service %s returned %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode pii service %s response: %w", path, err)
	}
	return nil
}

// Detect sends one message's text to the sidecar and returns its findings
// as gateway findings. IDs are left empty for the caller to assign.
func (s *PIIService) Detect(ctx context.Context, msg int, text string) ([]Finding, error) {
	var out struct {
		Findings []serviceFinding `json:"findings"`
	}
	if err := s.post(ctx, "/detect", text, &out); err != nil {
		return nil, err
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

// Redact sends text to the sidecar's POST /redact and returns the redacted
// text and the findings it replaced. Offsets refer to the original text.
func (s *PIIService) Redact(ctx context.Context, text string) (*RedactResult, error) {
	var out RedactResult
	if err := s.post(ctx, "/redact", text, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
