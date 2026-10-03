package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	policy, err := LoadBundle(context.Background(), "build/pol_ai_003-2.1.tar.gz", os.Getenv("POLICY_VERIFY_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	g := &Gateway{
		Policy:       policy,
		CheckTimeout: 100 * time.Millisecond,
		Client:       &http.Client{Timeout: 120 * time.Second},
		Log:          slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		// In the product these come from the governance database.
		Keys: map[string]Key{
			"gw-support-bot": {
				App:         App{ID: "support-bot"},
				Destination: Destination{Provider: "openai", Hosting: "external"},
				UpstreamURL: "https://api.openai.com",
				UpstreamKey: os.Getenv("OPENAI_API_KEY"),
			},
		},
	}
	http.Handle("POST /v1/chat/completions", g)
	log.Println("gateway listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
