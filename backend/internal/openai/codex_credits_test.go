package openai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestQueryQuotaResetCreditsIncludesCodexCredits(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		usageBody   string
		wantCredits string
	}{
		{"decimal balance", http.StatusOK, `{"plan_type":"plus","credits":{"has_credits":true,"unlimited":false,"balance":"12345678901234567890.0123"}}`, `{"has_credits":true,"unlimited":false,"balance":"12345678901234567890.0123"}`},
		{"zero", http.StatusOK, `{"credits":{"has_credits":false,"unlimited":false,"balance":"0"}}`, `{"has_credits":false,"unlimited":false,"balance":"0"}`},
		{"unlimited", http.StatusOK, `{"credits":{"has_credits":false,"unlimited":true,"balance":null}}`, `{"has_credits":false,"unlimited":true,"balance":null}`},
		{"hidden balance", http.StatusOK, `{"credits":{"has_credits":true,"unlimited":false}}`, `{"has_credits":true,"unlimited":false,"balance":null}`},
		{"missing credits", http.StatusOK, `{"plan_type":"plus"}`, `null`},
		{"upstream error", http.StatusInternalServerError, `{"credits":{"has_credits":true,"unlimited":false,"balance":"9"}}`, `null`},
		{"invalid json", http.StatusOK, `{"credits":`, `null`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			usageCalls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("method = %s", r.Method)
				}
				assertQuotaResetHeaders(t, r)
				body := `{"available_count":1,"credits":[{"reset_type":"codex_rate_limits","status":"available","expires_at":"2026-08-12T05:09:00Z"}]}`
				status := http.StatusOK
				switch r.URL.Path {
				case "/backend-api/wham/rate-limit-reset-credits":
				case "/backend-api/wham/usage":
					usageCalls++
					body, status = test.usageBody, test.status
				default:
					t.Fatalf("path = %s", r.URL.Path)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}

			credits, err := NewGateway(client).QueryQuotaResetCredits(context.Background(), "access-token", "account-id", "")
			if err != nil {
				t.Fatal(err)
			}
			if credits.AvailableCount != 1 || usageCalls != 1 {
				t.Fatalf("available = %d, usage calls = %d", credits.AvailableCount, usageCalls)
			}
			encoded, err := json.Marshal(credits.CodexCredits)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != test.wantCredits {
				t.Fatalf("codex credits = %s, want %s", encoded, test.wantCredits)
			}
		})
	}
}
