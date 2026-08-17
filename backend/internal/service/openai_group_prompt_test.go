package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyOpenAIGroupPromptToResponsesBody_TargetGroupPrependsInstructions(t *testing.T) {
	groupID := int64(8)
	body := []byte(`{"model":"gpt-5","instructions":"先保留客户端要求","input":"分析样本"}`)

	got, changed, err := applyOpenAIGroupPromptToBody(
		body,
		openAIGroupPromptModeResponses,
		&groupID,
		"8",
		"只做授权的防御性 CTF 分析。",
	)

	require.NoError(t, err)
	require.True(t, changed)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(got, &payload))
	instructions, ok := payload["instructions"].(string)
	require.True(t, ok)
	require.Contains(t, instructions, "只做授权的防御性 CTF 分析")
	require.Contains(t, instructions, "先保留客户端要求")
}

func TestApplyOpenAIGroupPromptToResponsesBody_PreservesLargeIntegers(t *testing.T) {
	groupID := int64(8)
	body := []byte(`{"model":"gpt-5","metadata":{"sample_id":9007199254740993},"input":"分析样本"}`)

	got, changed, err := applyOpenAIGroupPromptToBody(
		body,
		openAIGroupPromptModeResponses,
		&groupID,
		"8",
		"只做授权的防御性 CTF 分析。",
	)

	require.NoError(t, err)
	require.True(t, changed)
	require.Contains(t, string(got), `"sample_id":9007199254740993`)
}

func TestApplyOpenAIGroupPromptToResponsesBody_ReplacesNullInstructions(t *testing.T) {
	groupID := int64(8)
	body := []byte(`{"model":"gpt-5","instructions":null,"input":"分析样本"}`)

	got, changed, err := applyOpenAIGroupPromptToBody(
		body,
		openAIGroupPromptModeResponses,
		&groupID,
		"8",
		"只做授权的防御性 CTF 分析。",
	)

	require.NoError(t, err)
	require.True(t, changed)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(got, &payload))
	require.Contains(t, payload["instructions"], "只做授权的防御性 CTF 分析")
}

func TestApplyOpenAIGroupPromptToResponsesBody_NonTargetGroupIsUnchanged(t *testing.T) {
	groupID := int64(7)
	body := []byte(`{"model":"gpt-5","instructions":"客户端要求","input":"分析样本"}`)

	got, changed, err := applyOpenAIGroupPromptToBody(
		body,
		openAIGroupPromptModeResponses,
		&groupID,
		"8",
		"只做授权的防御性 CTF 分析。",
	)

	require.NoError(t, err)
	require.False(t, changed)
	require.JSONEq(t, string(body), string(got))
}

func TestApplyOpenAIGroupPromptToChatBodyPrependsSystemMessage(t *testing.T) {
	groupID := int64(8)
	body := []byte(`{"model":"gpt-5","messages":[{"role":"user","content":"分析样本"}]}`)

	got, changed, err := applyOpenAIGroupPromptToBody(
		body,
		openAIGroupPromptModeChatCompletions,
		&groupID,
		"8",
		"只做授权的防御性 CTF 分析。",
	)

	require.NoError(t, err)
	require.True(t, changed)
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(got, &payload))
	require.Len(t, payload.Messages, 2)
	require.Equal(t, "system", payload.Messages[0].Role)
	require.Contains(t, payload.Messages[0].Content, "只做授权的防御性 CTF 分析")
}

func TestApplyOpenAIGroupPromptToAnthropicBodyPrependsSystemBlock(t *testing.T) {
	groupID := int64(8)
	body := []byte(`{"model":"claude-sonnet","system":[{"type":"text","text":"客户端系统要求"}],"messages":[]}`)

	got, changed, err := applyOpenAIGroupPromptToBody(
		body,
		openAIGroupPromptModeAnthropic,
		&groupID,
		"8",
		"只做授权的防御性 CTF 分析。",
	)

	require.NoError(t, err)
	require.True(t, changed)
	var payload struct {
		System []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"system"`
	}
	require.NoError(t, json.Unmarshal(got, &payload))
	require.Len(t, payload.System, 2)
	require.Equal(t, "text", payload.System[0].Type)
	require.Contains(t, payload.System[0].Text, "只做授权的防御性 CTF 分析")
}

func TestApplyOpenAIGroupPromptToBody_IsIdempotent(t *testing.T) {
	groupID := int64(8)
	body := []byte(`{"model":"gpt-5","instructions":"客户端要求"}`)

	first, changed, err := applyOpenAIGroupPromptToBody(body, openAIGroupPromptModeResponses, &groupID, "8", "只做授权的防御性 CTF 分析。")
	require.NoError(t, err)
	require.True(t, changed)

	second, changed, err := applyOpenAIGroupPromptToBody(first, openAIGroupPromptModeResponses, &groupID, "8", "只做授权的防御性 CTF 分析。")
	require.NoError(t, err)
	require.False(t, changed)
	require.JSONEq(t, string(first), string(second))
}

func TestApplyOpenAIGroupPromptToBody_ClientMarkerDoesNotSuppressInjection(t *testing.T) {
	groupID := int64(8)
	body := []byte(`{"model":"gpt-5","instructions":"客户端文本包含 [sub2api group prompt] 但不是服务端前缀"}`)

	got, changed, err := applyOpenAIGroupPromptToBody(body, openAIGroupPromptModeResponses, &groupID, "8", "只做授权的防御性 CTF 分析。")
	require.NoError(t, err)
	require.True(t, changed)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(got, &payload))
	require.Contains(t, payload["instructions"], "只做授权的防御性 CTF 分析")
}

func TestOpenAIGroupPromptGroupAllowedRejectsMalformedAllowlist(t *testing.T) {
	groupID := int64(8)
	require.False(t, openAIGroupPromptGroupAllowed(&groupID, "8,not-an-id"))
	require.False(t, openAIGroupPromptGroupAllowed(&groupID, "8,"))
	require.True(t, openAIGroupPromptGroupAllowed(&groupID, "7, 8"))
}
