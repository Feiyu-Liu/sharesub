package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/imroc/req/v3"
	"github.com/sharesub/sharesub/backend/internal/application"
)

const (
	clientID        = "app_EMoamEEZ73f0CkXaXp7hrann"
	authorizeURL    = "https://auth.openai.com/oauth/authorize"
	tokenURL        = "https://auth.openai.com/oauth/token"
	subscriptionURL = "https://chatgpt.com/backend-api/subscriptions"
	scopes          = "openid profile email offline_access"
	refreshScope    = "openid profile email"
)

type OAuthClient struct {
	client             *req.Client
	subscriptionClient *req.Client
	tokenURL           string
	now                func() time.Time
}

func NewOAuthClient(proxyURL string) *OAuthClient {
	client := req.C().SetTimeout(120 * time.Second).ImpersonateChrome()
	if proxyURL != "" {
		client.SetProxyURL(proxyURL)
	}
	return &OAuthClient{
		client:             client,
		subscriptionClient: newSubscriptionClient(proxyURL),
		tokenURL:           tokenURL,
		now:                time.Now,
	}
}

func (c *OAuthClient) AuthorizationURL(state, challenge, redirectURI string) string {
	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", clientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", scopes)
	params.Set("state", state)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	return authorizeURL + "?" + params.Encode()
}

func (c *OAuthClient) Exchange(ctx context.Context, code, verifier, redirectURI string) (application.OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", clientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	token, err := c.requestToken(ctx, form)
	if err != nil {
		return application.OAuthToken{}, err
	}
	if token.RefreshToken == "" || token.Email == "" || token.ChatGPTAccountID == "" {
		return application.OAuthToken{}, errors.New("OpenAI authorization response is missing required account fields")
	}
	return token, nil
}

func (c *OAuthClient) Refresh(ctx context.Context, refreshToken string) (application.OAuthToken, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)
	form.Set("scope", refreshScope)
	token, err := c.requestToken(ctx, form)
	if err != nil {
		return application.OAuthToken{}, err
	}
	if token.RefreshToken == "" {
		token.RefreshToken = refreshToken
	}
	return token, nil
}

// Subscription traffic uses a separate browser fingerprint from OAuth token requests.
func newSubscriptionClient(proxyURL string) *req.Client {
	client := req.C().SetTimeout(120 * time.Second).ImpersonateFirefox()
	if proxyURL != "" {
		client.SetProxyURL(proxyURL)
	}
	return client
}

func (c *OAuthClient) SubscriptionExpiresAt(ctx context.Context, accessToken, accountID, proxyURL string) (*time.Time, error) {
	client := c.subscriptionClient
	if proxyURL != "" {
		client = newSubscriptionClient(proxyURL)
		defer client.GetTransport().CloseIdleConnections()
	}
	var subscription struct {
		PlanType    string `json:"plan_type"`
		ActiveUntil string `json:"active_until"`
		WillRenew   bool   `json:"will_renew"`
		ID          string `json:"id"`
	}
	resp, err := client.R().
		SetContext(ctx).
		SetHeader("Authorization", "Bearer "+accessToken).
		SetHeader("Origin", "https://chatgpt.com").
		SetHeader("Referer", "https://chatgpt.com/").
		SetHeader("Accept", "application/json").
		SetQueryParam("account_id", accountID).
		SetSuccessResult(&subscription).
		Get(subscriptionURL)
	status := 0
	challenge := false
	if resp != nil && resp.Response != nil {
		status = resp.StatusCode
		challenge = strings.EqualFold(strings.TrimSpace(resp.Header.Get("Cf-Mitigated")), "challenge")
	}
	if challenge || (status != 0 && (status < 200 || status >= 300)) {
		return nil, &application.SubscriptionQueryError{Kind: "http", StatusCode: status, CloudflareChallenge: challenge}
	}
	if err != nil {
		kind := "network"
		if status != 0 {
			kind = "response"
		}
		return nil, &application.SubscriptionQueryError{Kind: kind, StatusCode: status, Cause: err}
	}
	if subscription.ActiveUntil == "" {
		return nil, nil
	}
	expiresAt, err := time.Parse(time.RFC3339, subscription.ActiveUntil)
	if err != nil {
		return nil, &application.SubscriptionQueryError{Kind: "response", StatusCode: status, Cause: err}
	}
	return &expiresAt, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

type idTokenClaims struct {
	Email      string `json:"email"`
	OpenAIAuth struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
		ChatGPTPlanType  string `json:"chatgpt_plan_type"`
	} `json:"https://api.openai.com/auth"`
}

func (c *OAuthClient) requestToken(ctx context.Context, form url.Values) (application.OAuthToken, error) {
	var token tokenResponse
	resp, err := c.client.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetHeaders(codexOAuthCredentialIdentity()).
		SetFormDataFromValues(form).
		SetSuccessResult(&token).
		Post(c.tokenURL)
	if err != nil {
		return application.OAuthToken{}, fmt.Errorf("OpenAI OAuth request: %w", err)
	}
	if !resp.IsSuccessState() {
		return application.OAuthToken{}, fmt.Errorf("OpenAI OAuth returned status %d", resp.StatusCode)
	}
	if token.AccessToken == "" || token.ExpiresIn <= 0 {
		return application.OAuthToken{}, errors.New("OpenAI OAuth response is missing required token fields")
	}
	var claims idTokenClaims
	if token.IDToken != "" {
		claims, err = decodeIDToken(token.IDToken)
		if err != nil {
			return application.OAuthToken{}, err
		}
	}
	return application.OAuthToken{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken, IDToken: token.IDToken,
		ExpiresAt: c.now().Add(time.Duration(token.ExpiresIn) * time.Second), Email: claims.Email,
		ChatGPTAccountID: claims.OpenAIAuth.ChatGPTAccountID, PlanType: claims.OpenAIAuth.ChatGPTPlanType,
	}, nil
}

func decodeIDToken(raw string) (idTokenClaims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return idTokenClaims{}, errors.New("OpenAI ID token has invalid JWT format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return idTokenClaims{}, fmt.Errorf("decode OpenAI ID token payload: %w", err)
	}
	var claims idTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return idTokenClaims{}, fmt.Errorf("parse OpenAI ID token payload: %w", err)
	}
	return claims, nil
}
