package domain

import "slices"

// RequestTimezones is the curated set of IANA zones an account may present to
// the upstream when request timezone rewriting is enabled. An empty account
// value disables rewriting.
var RequestTimezones = []string{
	"Asia/Singapore",
	"Asia/Tokyo",
	"Asia/Seoul",
	"America/Los_Angeles",
	"America/New_York",
	"Europe/London",
}

// RequestLocaleAcceptLanguage replaces a client Accept-Language header while
// request timezone rewriting is enabled.
const RequestLocaleAcceptLanguage = "en-US,en;q=0.9"

func IsRequestTimezone(value string) bool {
	return slices.Contains(RequestTimezones, value)
}
