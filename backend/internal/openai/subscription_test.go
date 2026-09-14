package openai

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/imroc/req/v3"
	"github.com/sharesub/sharesub/backend/internal/application"
)

func TestSubscriptionClientFingerprintAndProxy(t *testing.T) {
	for _, proxy := range []string{"", "http://user:password@localhost:8080", "socks5://localhost:1080"} {
		t.Run(proxy, func(t *testing.T) {
			oauth := NewOAuthClient(proxy)
			if oauth.client == oauth.subscriptionClient {
				t.Fatal("subscription shares token client")
			}
			if !strings.Contains(oauth.client.Headers.Get("User-Agent"), "Chrome/") {
				t.Fatal("token fingerprint changed")
			}
			for _, client := range []*req.Client{oauth.subscriptionClient, newSubscriptionClient(proxy)} {
				if !strings.Contains(client.Headers.Get("User-Agent"), "Firefox/") || strings.Contains(client.Headers.Get("User-Agent"), "Chrome/") {
					t.Fatal("subscription does not use Firefox")
				}
				if proxy != "" {
					request, _ := http.NewRequest(http.MethodGet, subscriptionURL, nil)
					got, err := client.GetTransport().Proxy(request)
					if err != nil || got.String() != proxy {
						t.Fatalf("proxy not preserved: %v", err)
					}
				}
			}
		})
	}
}

func TestSubscriptionQueryFailureClassification(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     int
		header     string
		body       string
		networkErr error
		kind       string
		challenge  bool
	}{
		{name: "challenge", status: 403, header: "challenge", body: "private upstream body", kind: "http", challenge: true},
		{name: "challenge503", status: 503, header: " CHALLENGE ", body: "private upstream body", kind: "http", challenge: true},
		{name: "plain403", status: 403, body: "cloudflare challenge", kind: "http"},
		{name: "http500", status: 500, body: "private upstream body", kind: "http"},
		{name: "network", networkErr: errors.New("http://user:secret@proxy.invalid"), kind: "network"},
		{name: "invalidJSON", status: 200, body: "private upstream body", kind: "response"},
		{name: "invalidDate", status: 200, body: `{"active_until":"private-date"}`, kind: "response"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := NewOAuthClient("")
			client.subscriptionClient.GetTransport().WrapRoundTripFunc(func(http.RoundTripper) req.HttpRoundTripFunc {
				return func(*http.Request) (*http.Response, error) {
					if tt.networkErr != nil {
						return nil, tt.networkErr
					}
					return &http.Response{StatusCode: tt.status, Header: http.Header{"Content-Type": []string{"application/json"}, "Cf-Mitigated": []string{tt.header}}, Body: io.NopCloser(strings.NewReader(tt.body))}, nil
				}
			})
			expiry, err := client.SubscriptionExpiresAt(context.Background(), "secret-token", "account", "")
			var classified *application.SubscriptionQueryError
			if expiry != nil || !errors.As(err, &classified) {
				t.Fatalf("expected classified error: %v", err)
			}
			if classified.Kind != tt.kind || classified.StatusCode != tt.status || classified.CloudflareChallenge != tt.challenge {
				t.Fatalf("classification: %v", err)
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe error: %v", err)
			}
			if tt.networkErr != nil && !errors.Is(err, tt.networkErr) {
				t.Fatal("network cause lost")
			}
		})
	}
}

func TestSubscriptionQueryUsesAccountProxy(t *testing.T) {
	requests := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.Host
		w.WriteHeader(http.StatusForbidden)
	}))
	defer proxy.Close()
	client := NewOAuthClient("")
	_, err := client.SubscriptionExpiresAt(context.Background(), "access", "account", proxy.URL)
	if err == nil {
		t.Fatal("expected CONNECT rejection")
	}
	select {
	case got := <-requests:
		if got != "CONNECT chatgpt.com:443" {
			t.Fatalf("proxy request = %q", got)
		}
	default:
		t.Fatal("subscription did not use account proxy")
	}
}
