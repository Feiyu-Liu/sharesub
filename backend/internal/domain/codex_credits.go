package domain

// CodexCredits is the spendable Codex credit balance reported by /wham/usage.
// It is separate from rate-limit reset credits. Balance keeps the upstream
// decimal string so fractional points are not rounded.
type CodexCredits struct {
	HasCredits bool    `json:"has_credits"`
	Unlimited  bool    `json:"unlimited"`
	Balance    *string `json:"balance"`
}
