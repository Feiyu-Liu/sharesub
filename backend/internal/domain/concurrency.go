package domain

import "time"

// A zero MaxConcurrency inherits the account ceiling. BaseLimit is never lent.
type MemberConcurrencyLimit struct {
	UserID         string `json:"user_id"`
	Username       string `json:"username"`
	BaseLimit      int    `json:"base_limit"`
	MaxConcurrency int    `json:"max_concurrency"`
}

type ConcurrencyPolicy struct {
	Enabled bool                     `json:"enabled"`
	Members []MemberConcurrencyLimit `json:"members"`
}

type PlanConcurrencyContext struct {
	Plan       Plan
	AccountMax int
	Policy     ConcurrencyPolicy
}

type ConcurrencyMemberSample struct {
	UserID         string  `json:"user_id"`
	Username       string  `json:"username"`
	RequestSeconds float64 `json:"request_seconds"`
}

type ConcurrencyMinute struct {
	InstanceID      string
	AccountID       string
	PlanID          string
	Generation      int64
	BucketStart     time.Time
	ObservedSeconds float64
	Peak            int
	Members         []ConcurrencyMemberSample
}

type ConcurrencyPoint struct {
	BucketStart     time.Time `json:"bucket_start"`
	ObservedSeconds float64   `json:"observed_seconds"`
	Average         float64   `json:"average"`
	Peak            int       `json:"peak"`
}

type MemberConcurrencyTrend struct {
	UserID   string    `json:"user_id"`
	Username string    `json:"username"`
	Current  int       `json:"current"`
	Average  []float64 `json:"average"`
}

type PlanConcurrency struct {
	AccountID  string                   `json:"account_id"`
	AccountMax int                      `json:"account_max"`
	Policy     ConcurrencyPolicy        `json:"policy"`
	Current    int                      `json:"current"`
	Peak       int                      `json:"peak"`
	UpdatedAt  time.Time                `json:"updated_at"`
	Points     []ConcurrencyPoint       `json:"points"`
	Members    []MemberConcurrencyTrend `json:"members"`
}
