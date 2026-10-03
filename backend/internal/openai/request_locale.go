package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode"

	"github.com/sharesub/sharesub/backend/internal/domain"
)

var (
	requestLocaleNow     = time.Now
	requestLocaleDateRaw = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// rewriteRequestLocale presents the account's configured timezone upstream.
// Only the Codex environment_context timezone/current_date values and the
// web_search user_location timezone are touched; every other byte of input and
// tools stays as sent, and an unchanged request is returned verbatim.
func rewriteRequestLocale(body []byte, timezone string) ([]byte, error) {
	if timezone == "" {
		return body, nil
	}
	if !domain.IsRequestTimezone(timezone) {
		return nil, fmt.Errorf("unsupported request timezone %q", timezone)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load request timezone %q: %w", timezone, err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("parse Codex request for timezone rewrite: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("Codex request must be a JSON object")
	}
	rewrite := requestLocaleRewrite{timezone: timezone, date: requestLocaleNow().In(location).Format("2006-01-02")}
	changed := false
	if raw, ok := rewrite.input(payload["input"]); ok {
		payload["input"], changed = raw, true
	}
	if raw, ok := rewrite.tools(payload["tools"]); ok {
		payload["tools"], changed = raw, true
	}
	if !changed {
		return body, nil
	}
	return json.Marshal(payload)
}

// applyRequestLocaleHeaders only replaces a client-supplied Accept-Language so
// requests that never carried one keep their original header shape.
func applyRequestLocaleHeaders(headers http.Header, timezone string) {
	if timezone == "" || len(headers.Values("Accept-Language")) == 0 {
		return
	}
	headers.Set("Accept-Language", domain.RequestLocaleAcceptLanguage)
}

type requestLocaleRewrite struct {
	timezone string
	date     string
}

func (r requestLocaleRewrite) input(raw json.RawMessage) (json.RawMessage, bool) {
	var items []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &items) != nil {
		return nil, false
	}
	changed := false
	for index, item := range items {
		var message map[string]json.RawMessage
		var role string
		if json.Unmarshal(item, &message) != nil || message == nil || json.Unmarshal(message["role"], &role) != nil || role != "user" {
			continue
		}
		if content, ok := r.content(message["content"]); ok {
			message["content"] = content
			items[index], _ = json.Marshal(message)
			changed = true
		}
	}
	if !changed {
		return nil, false
	}
	out, err := json.Marshal(items)
	return out, err == nil
}

func (r requestLocaleRewrite) content(raw json.RawMessage) (json.RawMessage, bool) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		next, ok := r.environmentContext(text)
		if !ok {
			return nil, false
		}
		out, err := json.Marshal(next)
		return out, err == nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) != nil {
		return nil, false
	}
	changed := false
	for index, value := range parts {
		var part map[string]json.RawMessage
		var partType string
		if json.Unmarshal(value, &part) != nil || part == nil || json.Unmarshal(part["type"], &partType) != nil || partType != "input_text" {
			continue
		}
		if json.Unmarshal(part["text"], &text) != nil {
			continue
		}
		next, ok := r.environmentContext(text)
		if !ok {
			continue
		}
		part["text"], _ = json.Marshal(next)
		parts[index], _ = json.Marshal(part)
		changed = true
	}
	if !changed {
		return nil, false
	}
	out, err := json.Marshal(parts)
	return out, err == nil
}

// environmentContext accepts only a text that is exactly one
// <environment_context> element so user prose mentioning the tags is never
// edited.
func (r requestLocaleRewrite) environmentContext(text string) (string, bool) {
	const open, closing = "<environment_context>", "</environment_context>"
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, open) || !strings.HasSuffix(trimmed, closing) || strings.Count(trimmed, open) != 1 || strings.Count(trimmed, closing) != 1 {
		return text, false
	}
	next, timezoneChanged := replaceRequestLocaleTag(text, "timezone", func(string) (string, bool) { return r.timezone, true })
	next, dateChanged := replaceRequestLocaleTag(next, "current_date", func(value string) (string, bool) {
		return r.date, requestLocaleDateRaw.MatchString(value)
	})
	return next, timezoneChanged || dateChanged
}

func replaceRequestLocaleTag(text, tag string, replace func(string) (string, bool)) (string, bool) {
	open, closing := "<"+tag+">", "</"+tag+">"
	if strings.Count(text, open) != 1 || strings.Count(text, closing) != 1 {
		return text, false
	}
	start := strings.Index(text, open) + len(open)
	end := strings.Index(text, closing)
	if end < start {
		return text, false
	}
	value := text[start:end]
	rest := strings.TrimLeftFunc(value, unicode.IsSpace)
	leading := value[:len(value)-len(rest)]
	trimmed := strings.TrimRightFunc(rest, unicode.IsSpace)
	trailing := rest[len(trimmed):]
	next, ok := replace(trimmed)
	if !ok || next == trimmed {
		return text, false
	}
	return text[:start] + leading + next + trailing + text[end:], true
}

func (r requestLocaleRewrite) tools(raw json.RawMessage) (json.RawMessage, bool) {
	var tools []json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &tools) != nil {
		return nil, false
	}
	changed := false
	for index, value := range tools {
		var tool map[string]json.RawMessage
		var toolType string
		if json.Unmarshal(value, &tool) != nil || tool == nil || json.Unmarshal(tool["type"], &toolType) != nil || !strings.HasPrefix(toolType, "web_search") {
			continue
		}
		var location map[string]json.RawMessage
		if json.Unmarshal(tool["user_location"], &location) != nil || location == nil {
			continue
		}
		var current string
		if json.Unmarshal(location["timezone"], &current) == nil && current == r.timezone {
			continue
		}
		location["timezone"], _ = json.Marshal(r.timezone)
		tool["user_location"], _ = json.Marshal(location)
		tools[index], _ = json.Marshal(tool)
		changed = true
	}
	if !changed {
		return nil, false
	}
	out, err := json.Marshal(tools)
	return out, err == nil
}
