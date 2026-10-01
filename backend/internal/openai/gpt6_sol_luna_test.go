package openai

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestGPT6SolLunaPreserveSupportedReasoningAcrossProtocols(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna"} {
		for _, effort := range []string{"none", "low", "medium", "high", "xhigh", "max"} {
			for _, protocol := range []string{"responses", "compact", "websocket"} {
				t.Run(model+"/"+effort+"/"+protocol, func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"type":"response.create","model":%q,"input":[],"reasoning":{"effort":%q}}`, model, effort))
					var normalized []byte
					var metadata RequestBilling
					var err error
					if protocol == "websocket" {
						normalized, metadata, _, err = prepareResponsesWebSocketFrame(body, 1, "")
					} else {
						normalized, metadata, err = PrepareRequest(body, protocol == "compact")
					}
					if err != nil {
						t.Fatal(err)
					}
					var payload struct {
						Model     string
						Reasoning struct{ Effort string }
					}
					if err := json.Unmarshal(normalized, &payload); err != nil {
						t.Fatal(err)
					}
					if payload.Model != model || metadata.Model != model || payload.Reasoning.Effort != effort {
						t.Fatalf("model/effort changed: %s metadata=%+v", normalized, metadata)
					}
				})
			}
		}
	}
}

func TestGPT61SolRejectsDisabledReasoning(t *testing.T) {
	for _, effort := range []string{"none", "minimal"} {
		if _, _, err := PrepareRequest([]byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","input":[],"reasoning":{"effort":%q}}`, effort)), false); err == nil {
			t.Fatalf("expected effort %q to be rejected", effort)
		}
	}
	for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
		body, _, err := PrepareRequest([]byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","input":[],"reasoning":{"effort":%q},"temperature":0.2}`, effort)), false)
		if err != nil {
			t.Fatal(err)
		}
		if string(body) == "" || string(body) == "{}" {
			t.Fatalf("unexpected normalized body: %s", body)
		}
	}
}
