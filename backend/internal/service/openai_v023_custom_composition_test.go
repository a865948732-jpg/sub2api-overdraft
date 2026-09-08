//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestV023LandlordModelAllowlistComposesWithPromptAndOverdraft(t *testing.T) {
	t.Cleanup(func() { SetCodexQuotaOverdraftEnabled(false) })
	SetCodexQuotaOverdraftEnabled(true)

	groupID := int64(5)
	group := &Group{
		ID:                 groupID,
		Platform:           PlatformOpenAI,
		Status:             StatusActive,
		Hydrated:           true,
		ForceOpenAIFast:    true,
		FreeOpenAIFast:     true,
		MaxReasoningEffort: "medium",
		ModelAllowlist: GroupModelAllowlist{
			Enabled: true,
			Models:  []string{"gpt-5.6-sol"},
		},
	}
	require.True(t, group.ModelAllowlistEnabled())
	require.True(t, group.ModelAllowlist.Allows("gpt-5.6-sol"))
	require.False(t, group.ModelAllowlist.Allows("gpt-5.4"))

	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx = WithCodexQuotaOverdraftScheduling(ctx)

	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	svc.cfg = &config.Config{Gateway: config.GatewayConfig{CodexQuotaOverdraftEnabled: true}}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	body := []byte(`{"model":"gpt-5.6-sol","reasoning":{"effort":"max"},"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)

	var changed bool
	var err error
	body, changed, err = ApplyOpenAIReasoningEffortPolicy(
		body,
		group.MaxReasoningEffort,
		nil,
		ReasoningEffortOverLimitDowngrade,
	)
	require.NoError(t, err)
	require.True(t, changed)

	body, changed, err = applyOpenAIGroupPromptToBody(
		body,
		openAIGroupPromptModeResponses,
		&groupID,
		"5",
		"Only handle the authorized competition task.",
	)
	require.NoError(t, err)
	require.True(t, changed)

	body, err = svc.applyOpenAIFastPolicyToBody(ctx, account, "gpt-5.6-sol", body)
	require.NoError(t, err)
	body = svc.prepareCodexQuotaOverdraftBody(ctx, account, false, body)

	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(body, "model").String())
	require.Equal(t, "medium", gjson.GetBytes(body, "reasoning.effort").String())
	require.Contains(t, gjson.GetBytes(body, "instructions").String(), "authorized competition task")
	require.Equal(t, OpenAIFastTierPriority, gjson.GetBytes(body, "service_tier").String())
	require.Equal(t, int64(3), gjson.GetBytes(body, "input.#").Int())
	require.Equal(t, "custom_tool_call", gjson.GetBytes(body, "input.1.type").String())
	require.Equal(t, "custom_tool_call_output", gjson.GetBytes(body, "input.2.type").String())
}

func TestV023LandlordDisabledModelAllowlistPreservesLegacyRouting(t *testing.T) {
	group := &Group{
		ID:             5,
		Platform:       PlatformOpenAI,
		Status:         StatusActive,
		Hydrated:       true,
		ModelAllowlist: GroupModelAllowlist{},
	}

	require.False(t, group.ModelAllowlistEnabled())
	require.True(t, group.ModelAllowlist.Allows("gpt-5.6-sol"))
	require.True(t, group.ModelAllowlist.Allows("future-model-not-yet-listed"))
}
