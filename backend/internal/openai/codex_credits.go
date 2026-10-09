package openai

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sharesub/sharesub/backend/internal/domain"
)

const (
	chatGPTUsageURL          = "https://chatgpt.com/backend-api/wham/usage"
	codexCreditsQueryTimeout = 10 * time.Second
)

type codexUsageCreditsPayload struct {
	Credits *domain.CodexCredits `json:"credits"`
}

// queryCodexCredits reads only the credits block of /wham/usage. A missing
// block returns nil without error because the upstream omits it for some plans.
func (g *Gateway) queryCodexCredits(ctx context.Context, accessToken, chatgptAccountID, proxyURL string) (*domain.CodexCredits, error) {
	requestCtx, cancel := context.WithTimeout(ctx, codexCreditsQueryTimeout)
	defer cancel()
	req, err := newQuotaResetRequest(requestCtx, http.MethodGet, g.usageURL, accessToken, chatgptAccountID, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.doQuotaResetRequest(req, proxyURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("query codex usage returned status %d", resp.StatusCode)
	}
	var payload codexUsageCreditsPayload
	if err := decodeQuotaResetResponse(resp.Body, &payload); err != nil {
		return nil, fmt.Errorf("decode codex usage: %w", err)
	}
	return payload.Credits, nil
}
