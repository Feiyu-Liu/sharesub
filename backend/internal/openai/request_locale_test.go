package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testEnvironmentContext = "<environment_context>\n  <cwd>/repo</cwd>\n  <shell>zsh</shell>\n  <current_date>2026-10-02</current_date>\n  <timezone>Asia/Shanghai</timezone>\n</environment_context>"

func withRequestLocaleNow(t *testing.T, now time.Time) {
	t.Helper()
	previous := requestLocaleNow
	requestLocaleNow = func() time.Time { return now }
	t.Cleanup(func() { requestLocaleNow = previous })
}

func requestLocaleBody(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// decodedRequestLocaleBody unescapes JSON string content so assertions do not
// depend on json.Marshal's HTML escaping of environment_context tags.
func decodedRequestLocaleBody(t *testing.T, body []byte) string {
	t.Helper()
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(payload)
	return out.String()
}

func TestRewriteRequestLocale(t *testing.T) {
	// 2026-10-02 17:30 UTC is already 2026-10-03 in Singapore and Tokyo.
	withRequestLocaleNow(t, time.Date(2026, 10, 2, 17, 30, 0, 0, time.UTC))
	rewritten := strings.NewReplacer("2026-10-02", "2026-10-03", "Asia/Shanghai", "Asia/Singapore").Replace(testEnvironmentContext)
	userText := "Explain <environment_context><timezone>Asia/Shanghai</timezone></environment_context> please"
	tests := []struct {
		name     string
		timezone string
		payload  map[string]any
		want     map[string]any
	}{
		{
			name:     "string content",
			timezone: "Asia/Singapore",
			payload:  map[string]any{"input": []any{map[string]any{"type": "message", "role": "user", "content": testEnvironmentContext}}},
			want:     map[string]any{"input": []any{map[string]any{"type": "message", "role": "user", "content": rewritten}}},
		},
		{
			name:     "input_text parts",
			timezone: "Asia/Singapore",
			payload: map[string]any{"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "hello"},
				map[string]any{"type": "input_text", "text": "\n" + testEnvironmentContext + "\n"},
			}}}},
			want: map[string]any{"input": []any{map[string]any{"type": "message", "role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "hello"},
				map[string]any{"type": "input_text", "text": "\n" + rewritten + "\n"},
			}}}},
		},
		{
			name:     "non-user roles and prose untouched",
			timezone: "Asia/Singapore",
			payload: map[string]any{"input": []any{
				map[string]any{"type": "message", "role": "developer", "content": testEnvironmentContext},
				map[string]any{"type": "message", "role": "user", "content": userText},
			}},
		},
		{
			name:     "missing timezone only updates date",
			timezone: "Asia/Tokyo",
			payload:  map[string]any{"input": []any{map[string]any{"role": "user", "content": "<environment_context><current_date>2026-10-02</current_date></environment_context>"}}},
			want:     map[string]any{"input": []any{map[string]any{"role": "user", "content": "<environment_context><current_date>2026-10-03</current_date></environment_context>"}}},
		},
		{
			name:     "non-date current_date untouched",
			timezone: "America/Los_Angeles",
			payload:  map[string]any{"input": []any{map[string]any{"role": "user", "content": "<environment_context><current_date>Friday</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}}},
			want:     map[string]any{"input": []any{map[string]any{"role": "user", "content": "<environment_context><current_date>Friday</current_date><timezone>America/Los_Angeles</timezone></environment_context>"}}},
		},
		{
			name:     "web_search user_location",
			timezone: "Asia/Seoul",
			payload: map[string]any{"tools": []any{
				map[string]any{"type": "web_search", "user_location": map[string]any{"type": "approximate", "country": "US", "timezone": "Asia/Shanghai"}},
				map[string]any{"type": "web_search_preview"},
				map[string]any{"type": "function", "name": "lookup", "user_location": map[string]any{"timezone": "Asia/Shanghai"}},
			}},
			want: map[string]any{"tools": []any{
				map[string]any{"type": "web_search", "user_location": map[string]any{"type": "approximate", "country": "US", "timezone": "Asia/Seoul"}},
				map[string]any{"type": "web_search_preview"},
				map[string]any{"type": "function", "name": "lookup", "user_location": map[string]any{"timezone": "Asia/Shanghai"}},
			}},
		},
		{
			name:    "disabled",
			payload: map[string]any{"input": []any{map[string]any{"role": "user", "content": testEnvironmentContext}}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := requestLocaleBody(t, test.payload)
			got, err := rewriteRequestLocale(body, test.timezone)
			if err != nil {
				t.Fatal(err)
			}
			if test.want == nil {
				if !bytes.Equal(got, body) {
					t.Fatalf("body changed:\n%s\n%s", body, got)
				}
				return
			}
			var gotPayload, wantPayload any
			if err := json.Unmarshal(got, &gotPayload); err != nil {
				t.Fatal(err)
			}
			_ = json.Unmarshal(requestLocaleBody(t, test.want), &wantPayload)
			gotJSON, _ := json.Marshal(gotPayload)
			wantJSON, _ := json.Marshal(wantPayload)
			if !bytes.Equal(gotJSON, wantJSON) {
				t.Fatalf("body = %s\nwant %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestRewriteRequestLocalePreservesLargeIntegersAndRejectsUnlistedZones(t *testing.T) {
	body := []byte(`{"max_output_tokens":9007199254740993,"input":[{"role":"user","content":"<environment_context><timezone>Asia/Shanghai</timezone></environment_context>","large":9007199254740995}]}`)
	got, err := rewriteRequestLocale(body, "Europe/London")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("9007199254740993")) || !bytes.Contains(got, []byte("9007199254740995")) || !strings.Contains(decodedRequestLocaleBody(t, got), "<timezone>Europe/London</timezone>") {
		t.Fatalf("body = %s", got)
	}
	if _, err := rewriteRequestLocale(body, "Asia/Shanghai"); err == nil {
		t.Fatal("unlisted timezone was accepted")
	}
}

func TestForwardRewritesRequestLocale(t *testing.T) {
	withRequestLocaleNow(t, time.Date(2026, 10, 2, 17, 30, 0, 0, time.UTC))
	body := []byte(`{"model":"gpt-5.5","stream":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context><current_date>2026-10-02</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}]}]}`)
	for _, test := range []struct {
		path    string
		rewrite bool
	}{
		{path: "/v1/responses", rewrite: true},
		{path: "/v1/responses/compact", rewrite: true},
		{path: "/v1/images/generations"},
	} {
		t.Run(test.path, func(t *testing.T) {
			var captured *http.Request
			var capturedBody []byte
			gateway := NewGateway(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				captured = req
				capturedBody, _ = io.ReadAll(req.Body)
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: req}, nil
			})})
			inbound := httptest.NewRequest(http.MethodPost, "http://gateway.test"+test.path, nil)
			inbound.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
			response, err := gateway.Forward(context.Background(), inbound, body, RequestBilling{Model: "gpt-5.5", PromptCacheKey: "session"}, "token", "upstream", "key", "", CodexFingerprintContext{AccountID: "account", Mode: "off", RequestTimezone: "Asia/Singapore"})
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			rewritten := strings.Contains(decodedRequestLocaleBody(t, capturedBody), "<current_date>2026-10-03</current_date><timezone>Asia/Singapore</timezone>")
			language := captured.Header.Get("Accept-Language")
			if test.rewrite && (!rewritten || language != "en-US,en;q=0.9") {
				t.Fatalf("not rewritten: language=%q body=%s", language, capturedBody)
			}
			if !test.rewrite && (rewritten || language != "zh-CN,zh;q=0.9") {
				t.Fatalf("images request rewritten: language=%q body=%s", language, capturedBody)
			}
		})
	}
}

func TestRequestLocaleHTTPAndWebSocketParity(t *testing.T) {
	withRequestLocaleNow(t, time.Date(2026, 10, 2, 17, 30, 0, 0, time.UTC))
	original := []byte(`{"type":"response.create","model":"gpt-5.5","input":[{"type":"message","role":"user","content":"<environment_context><current_date>2026-10-02</current_date><timezone>Asia/Shanghai</timezone></environment_context>"}],"tools":[{"type":"web_search","user_location":{"type":"approximate","timezone":"Asia/Shanghai"}}]}`)
	inbound := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", nil)
	inbound.Header.Set("Accept-Language", "zh-CN")
	var httpHeader http.Header
	var httpBody []byte
	gateway := NewGateway(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		httpHeader = req.Header.Clone()
		httpBody, _ = io.ReadAll(req.Body)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})})
	for _, account := range []string{"account", ""} {
		resp, err := gateway.Forward(context.Background(), inbound, original, RequestBilling{Model: "gpt-5.5"}, "token", "upstream", "key", "", CodexFingerprintContext{AccountID: account, Mode: "off", RequestTimezone: "Asia/Tokyo"})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		config := ResponsesWebSocketDialConfig{InternalAccountID: account, APIKeyID: "key", FingerprintMode: "off", RequestTimezone: "Asia/Tokyo", InboundHeader: inbound.Header, AccessToken: "token"}
		frame, err := PrepareResponsesWebSocketFingerprint(&config, original, "")
		if err != nil {
			t.Fatal(err)
		}
		headers, err := responsesWebSocketHeaders(config, "")
		if err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string][]byte{"http": httpBody, "websocket": frame} {
			var payload struct {
				Input []struct {
					Content string `json:"content"`
				} `json:"input"`
				Tools []struct {
					UserLocation map[string]string `json:"user_location"`
				} `json:"tools"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Input[0].Content != "<environment_context><current_date>2026-10-03</current_date><timezone>Asia/Tokyo</timezone></environment_context>" || payload.Tools[0].UserLocation["timezone"] != "Asia/Tokyo" {
				t.Fatalf("%s account=%q body = %s", name, account, body)
			}
		}
		if httpHeader.Get("Accept-Language") != "en-US,en;q=0.9" || headers.Get("Accept-Language") != "en-US,en;q=0.9" {
			t.Fatalf("Accept-Language http=%q websocket=%q", httpHeader.Get("Accept-Language"), headers.Get("Accept-Language"))
		}
	}
}
