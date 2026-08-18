package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// openAIGroupPromptMode identifies the wire format that needs a server-side
// group prompt. The prompt is intentionally injected at the gateway boundary
// so HTTP Responses, Chat Completions, Anthropic compatibility, and WebSocket
// ingress share the same allowlist decision.
type openAIGroupPromptMode string

const (
	openAIGroupPromptModeResponses       openAIGroupPromptMode = "responses"
	openAIGroupPromptModeChatCompletions openAIGroupPromptMode = "chat_completions"
	openAIGroupPromptModeAnthropic       openAIGroupPromptMode = "anthropic"
	openAIGroupPromptMarker                                    = "[sub2api group prompt]"
)

// applyOpenAIGroupPromptToBody injects a read-only server prompt only when the
// request's API-key group is explicitly listed in allowedGroupIDs. Empty or
// malformed allowlists fail closed. The marker makes retries/failover paths
// idempotent even when a body is passed through more than one adapter.
func applyOpenAIGroupPromptToBody(
	body []byte,
	mode openAIGroupPromptMode,
	groupID *int64,
	allowedGroupIDs string,
	prompt string,
) ([]byte, bool, error) {
	if !openAIGroupPromptGroupAllowed(groupID, allowedGroupIDs) {
		return body, false, nil
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return body, false, nil
	}
	serverPrompt := formatOpenAIGroupPrompt(prompt)

	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return nil, false, fmt.Errorf("parse group prompt request body: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("multiple JSON values")
		}
		return nil, false, fmt.Errorf("parse group prompt request body: %w", err)
	}
	if root == nil {
		return body, false, nil
	}

	changed := false
	switch mode {
	case openAIGroupPromptModeResponses:
		changed = applyResponsesGroupPrompt(root, serverPrompt)
	case openAIGroupPromptModeChatCompletions:
		changed = applyChatCompletionsGroupPrompt(root, serverPrompt)
	case openAIGroupPromptModeAnthropic:
		changed = applyAnthropicGroupPrompt(root, serverPrompt)
	default:
		return body, false, nil
	}
	if !changed {
		return body, false, nil
	}

	updated, err := json.Marshal(root)
	if err != nil {
		return nil, false, fmt.Errorf("marshal group prompt request body: %w", err)
	}
	return updated, true, nil
}

func openAIGroupPromptGroupAllowed(groupID *int64, allowedGroupIDs string) bool {
	if groupID == nil || *groupID <= 0 {
		return false
	}
	allowed := false
	for _, raw := range strings.Split(allowedGroupIDs, ",") {
		token := strings.TrimSpace(raw)
		if token == "" {
			return false
		}
		id, err := strconv.ParseInt(token, 10, 64)
		if err != nil || id <= 0 {
			return false
		}
		if id == *groupID {
			allowed = true
		}
	}
	return allowed
}

func formatOpenAIGroupPrompt(prompt string) string {
	if hasOpenAIGroupPromptMarkerPrefix(prompt) {
		return prompt
	}
	return openAIGroupPromptMarker + "\n" + prompt
}

func combineOpenAIGroupPrompt(existing, serverPrompt string) string {
	existing = strings.TrimSpace(existing)
	if existing == "" {
		return serverPrompt
	}
	return serverPrompt + "\n\n--- client instructions begin ---\n" + existing
}

func hasOpenAIGroupPromptMarkerPrefix(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == openAIGroupPromptMarker || strings.HasPrefix(trimmed, openAIGroupPromptMarker+"\n")
}

func hasOpenAIGroupPromptPrefix(value, serverPrompt string) bool {
	trimmed := strings.TrimSpace(value)
	serverPrompt = strings.TrimSpace(serverPrompt)
	return serverPrompt != "" && hasOpenAIGroupPromptMarkerPrefix(trimmed) &&
		(trimmed == serverPrompt || strings.HasPrefix(trimmed, serverPrompt+"\n"))
}

func applyResponsesGroupPrompt(root map[string]any, serverPrompt string) bool {
	if value, ok := root["instructions"]; ok {
		if value == nil {
			root["instructions"] = serverPrompt
			return true
		}
		if existing, ok := value.(string); ok {
			if hasOpenAIGroupPromptPrefix(existing, serverPrompt) {
				return false
			}
			root["instructions"] = combineOpenAIGroupPrompt(existing, serverPrompt)
			return true
		}
		// The Responses API expects instructions to be a string. Preserve an
		// unusual value rather than replacing client data with an invalid type.
		return false
	}
	root["instructions"] = serverPrompt
	return true
}

func applyChatCompletionsGroupPrompt(root map[string]any, serverPrompt string) bool {
	if rawMessages, exists := root["messages"]; exists {
		messages, ok := rawMessages.([]any)
		if !ok {
			return false
		}
		if len(messages) > 0 && jsonMessageHasGroupPromptPrefix(messages[0], serverPrompt) {
			return false
		}
		injected := map[string]any{
			"role":    "system",
			"content": serverPrompt,
		}
		root["messages"] = append([]any{injected}, messages...)
		return true
	}

	// Some clients send Responses-shaped input to /v1/chat/completions. Keep
	// that body in Responses form instead of manufacturing a messages array.
	if _, hasInput := root["input"]; hasInput {
		return applyResponsesGroupPrompt(root, serverPrompt)
	}
	root["messages"] = []any{map[string]any{
		"role":    "system",
		"content": serverPrompt,
	}}
	return true
}

func applyAnthropicGroupPrompt(root map[string]any, serverPrompt string) bool {
	value, exists := root["system"]
	if !exists || value == nil {
		root["system"] = serverPrompt
		return true
	}
	switch existing := value.(type) {
	case string:
		if hasOpenAIGroupPromptPrefix(existing, serverPrompt) {
			return false
		}
		root["system"] = combineOpenAIGroupPrompt(existing, serverPrompt)
		return true
	case []any:
		if len(existing) > 0 && jsonMessageHasGroupPromptPrefix(existing[0], serverPrompt) {
			return false
		}
		root["system"] = append([]any{map[string]any{
			"type": "text",
			"text": serverPrompt,
		}}, existing...)
		return true
	default:
		return false
	}
}

func jsonMessageHasGroupPromptPrefix(value any, serverPrompt string) bool {
	message, ok := value.(map[string]any)
	if !ok {
		return false
	}
	if role, ok := message["role"].(string); ok && strings.EqualFold(role, "system") {
		text, ok := message["content"].(string)
		return ok && hasOpenAIGroupPromptPrefix(text, serverPrompt)
	}
	if blockType, ok := message["type"].(string); ok && blockType == "text" {
		text, ok := message["text"].(string)
		return ok && hasOpenAIGroupPromptPrefix(text, serverPrompt)
	}
	return false
}

func (s *OpenAIGatewayService) applyConfiguredOpenAIGroupPrompt(
	c *gin.Context,
	body []byte,
	mode openAIGroupPromptMode,
) ([]byte, error) {
	if s == nil || s.cfg == nil || c == nil {
		return body, nil
	}
	value, ok := c.Get("api_key")
	if !ok {
		return body, nil
	}
	apiKey, ok := value.(*APIKey)
	if !ok || apiKey == nil {
		return body, nil
	}
	updated, _, err := applyOpenAIGroupPromptToBody(
		body,
		mode,
		apiKey.GroupID,
		s.cfg.Gateway.OpenAIGroupPromptGroupIDs,
		s.cfg.Gateway.OpenAIGroupPrompt,
	)
	return updated, err
}
