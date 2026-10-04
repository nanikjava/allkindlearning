package main

import (
	"fmt"
	"sort"
	"strings"
)

var placeholder = map[string]string{
	"pii.email":        "EMAIL",
	"pii.phone":        "PHONE",
	"pii.national_id":  "NATIONAL_ID",
	"pii.name":         "NAME",
	"pii.address":      "ADDRESS",
	"pii.bank_account": "BANK_ACCOUNT",
	"secret.password":  "PASSWORD",
}

// Redact replaces the given findings with typed placeholders such as
// [EMAIL_1]. The same value always gets the same placeholder, so the model
// can still tell that two mentions refer to the same person.
//
// It returns a vault mapping placeholder -> original value. The vault stays
// in gateway memory for the life of the request, so the response can be
// restored for the user if the policy allows it. It is never logged.
func Redact(msgs []Message, findings []Finding, ids []string) (map[string]string, int) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	byMsg := map[int][]Finding{}
	for _, f := range findings {
		if want[f.ID] {
			byMsg[f.Message] = append(byMsg[f.Message], f)
		}
	}

	vault := map[string]string{}    // placeholder -> original
	assigned := map[string]string{} // original -> placeholder
	counter := map[string]int{}
	count := 0

	for mi, fs := range byMsg {
		// Replace from the end of the string backwards so earlier offsets stay valid.
		sort.Slice(fs, func(i, j int) bool { return fs[i].Start > fs[j].Start })
		s := msgs[mi].Content
		for _, f := range fs {
			orig := s[f.Start:f.End]
			ph, ok := assigned[orig]
			if !ok {
				label := placeholder[f.Type]
				if label == "" {
					label = "REDACTED"
				}
				counter[label]++
				ph = fmt.Sprintf("[%s_%d]", label, counter[label])
				assigned[orig] = ph
				vault[ph] = orig
			}
			s = s[:f.Start] + ph + s[f.End:]
			count++
		}
		msgs[mi].Content = s
	}
	return vault, count
}

// Restore puts original values back into model output. Only call it when
// the policy allows the end user to see the data (they sent it, after all).
func Restore(text string, vault map[string]string) string {
	for ph, orig := range vault {
		text = strings.ReplaceAll(text, ph, orig)
	}
	return text
}
