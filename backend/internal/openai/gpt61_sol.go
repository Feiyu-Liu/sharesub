package openai

import (
	"fmt"
	"strings"
)

func validateGPT61SolRequest(payload map[string]any, model string) error {
	canonical := strings.ToLower(strings.TrimSpace(model))
	if index := strings.LastIndex(canonical, "/"); index >= 0 {
		canonical = canonical[index+1:]
	}
	canonical = strings.ReplaceAll(canonical, "_", "-")
	if canonical != "gpt-6.1-sol" {
		return nil
	}
	if reasoning, ok := payload["reasoning"].(map[string]any); ok {
		if effort, ok := reasoning["effort"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(effort)) {
			case "none", "minimal":
				return fmt.Errorf("gpt-6.1-sol does not support reasoning effort %q; use low, medium, high, xhigh or max", effort)
			}
		}
	}
	return nil
}
