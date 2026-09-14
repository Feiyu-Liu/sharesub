package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"

	"github.com/sharesub/sharesub/backend/internal/domain"
)

func TestSubscriptionQueryLogsSafeDiagnostics(t *testing.T) {
	for _, tt := range []struct {
		name      string
		err       error
		kind      string
		status    int
		challenge bool
	}{
		{"challenge", &SubscriptionQueryError{Kind: "http", StatusCode: 403, CloudflareChallenge: true}, "http", 403, true},
		{"network", &SubscriptionQueryError{Kind: "network", Cause: errors.New("secret-proxy-password")}, "network", 0, false},
		{"untyped", errors.New("secret-access-token"), "query", 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			service := &Service{oauth: &tokenRefreshOAuth{subscriptionErr: tt.err}, logger: slog.New(slog.NewJSONHandler(&logs, nil))}
			_, err := service.queryAccountSubscription(context.Background(), domain.Account{ID: "local-account", ChatGPTAccountID: "workspace", ProxyURL: "secret-proxy-password"}, "secret-access-token", "token_refresh")
			if !errors.Is(err, tt.err) {
				t.Fatal("query error lost")
			}
			var event struct {
				Level     string
				AccountID string `json:"account_id"`
				Operation string
				Kind      string `json:"error_kind"`
				Status    int
				Challenge bool `json:"cf_challenge"`
			}
			if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if event.Level != "WARN" || event.AccountID != "local-account" || event.Operation != "token_refresh" || event.Kind != tt.kind || event.Status != tt.status || event.Challenge != tt.challenge {
				t.Fatalf("log: %s", logs.String())
			}
			if bytes.Contains(logs.Bytes(), []byte("secret")) {
				t.Fatal("credentials leaked")
			}
		})
	}
}
