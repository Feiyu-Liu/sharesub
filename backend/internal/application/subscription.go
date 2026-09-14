package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/sharesub/sharesub/backend/internal/domain"
)

// SubscriptionQueryError exposes diagnostics without embedding upstream bodies,
// URLs or credentials in its printable message.
type SubscriptionQueryError struct {
	Kind                string
	StatusCode          int
	CloudflareChallenge bool
	Cause               error
}

func (e *SubscriptionQueryError) Error() string {
	return fmt.Sprintf("OpenAI subscription query failed (kind=%s status=%d cf_challenge=%t)", e.Kind, e.StatusCode, e.CloudflareChallenge)
}

func (e *SubscriptionQueryError) Unwrap() error { return e.Cause }

func (s *Service) queryAccountSubscription(ctx context.Context, account domain.Account, accessToken, operation string) (*time.Time, error) {
	expiresAt, err := s.oauth.SubscriptionExpiresAt(ctx, accessToken, account.ChatGPTAccountID, account.ProxyURL)
	if err == nil {
		return expiresAt, nil
	}
	kind, status, challenge := "query", 0, false
	var queryErr *SubscriptionQueryError
	if errors.As(err, &queryErr) {
		kind, status, challenge = queryErr.Kind, queryErr.StatusCode, queryErr.CloudflareChallenge
	}
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}
	// Do not log the original error: transport errors can contain proxy credentials.
	logger.WarnContext(ctx, "OpenAI subscription query failed",
		"account_id", account.ID, "operation", operation,
		"error_kind", kind, "status", status, "cf_challenge", challenge)
	return nil, err
}
