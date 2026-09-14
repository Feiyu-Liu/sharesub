package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/sharesub/sharesub/backend/internal/application"
	"github.com/sharesub/sharesub/backend/internal/domain"
	"github.com/sharesub/sharesub/backend/internal/openai"
)

func TestConcurrencyRoutesRequireAuthentication(t *testing.T) {
	server := New(&application.Service{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	for _, prefix := range []string{"/api/plans/plan/concurrency", "/api/admin/plans/plan/concurrency"} {
		for _, method := range []string{http.MethodGet, http.MethodPut} {
			response := httptest.NewRecorder()
			server.Handler().ServeHTTP(response, httptest.NewRequest(method, prefix, strings.NewReader(`{"enabled":true,"members":[]}`)))
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s: %d", method, prefix, response.Code)
			}
		}
	}
}
func TestMemberConcurrencyGatewayAndWebSocketErrors(t *testing.T) {
	response := httptest.NewRecorder()
	writeGatewayDomainError(response, domain.ErrMemberConcurrency)
	if response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), `"code":"member_concurrency_limited"`) {
		t.Fatalf("HTTP error: %d %s", response.Code, response.Body.String())
	}
	var closeErr *openai.ResponsesWebSocketCloseError
	if !errors.As(responsesWebSocketAccessError(domain.ErrMemberConcurrency), &closeErr) || closeErr.StatusCode() != websocket.StatusTryAgainLater {
		t.Fatal("WebSocket member saturation should be retryable")
	}
}
