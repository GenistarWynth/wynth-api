package service

import (
	"context"
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyAccountPoolRuntimeSelection_CodeAssistProjectDetectedAndCached verifies that
// when a Gemini Code Assist OAuth account has NO cached project id:
//   - accountPoolDetectGeminiCodeAssistProject is called once with the runtime credential.
//   - info.RuntimeGeminiOAuthType is set to "code_assist".
//   - info.RuntimeGeminiProjectID is set to the detected project id.
//   - The project id is persisted to the account's token_state (cache populated).
//
// A subsequent selection on the same account must NOT call the detector again (cache hit).
func TestApplyAccountPoolRuntimeSelection_CodeAssistProjectDetectedAndCached(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	accountView := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:      "code-assist-no-project",
		OAuthType: AccountPoolGeminiOAuthTypeCodeAssist,
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "ya29.code-assist-token",
			ExpiresAt:   9999999999,
			// ProjectID intentionally empty — detection must fire.
		},
	})

	const detectedProject = "projects/detected-project-123"
	detectorCalls := 0
	origDetector := accountPoolDetectGeminiCodeAssistProject
	accountPoolDetectGeminiCodeAssistProject = func(_ context.Context, token, _ string) (string, error) {
		detectorCalls++
		assert.Equal(t, "ya29.code-assist-token", token, "detector must receive the runtime credential")
		return detectedProject, nil
	}
	t.Cleanup(func() { accountPoolDetectGeminiCodeAssistProject = origDetector })

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "AIzaSy-channel-key",
			UpstreamModelName: "gemini-2.5-pro",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)
	require.NoError(t, err)
	defer ReleaseAccountPoolRuntimeSelection(ctx)

	assert.Equal(t, AccountPoolGeminiOAuthTypeCodeAssist, info.RuntimeGeminiOAuthType)
	assert.Equal(t, detectedProject, info.RuntimeGeminiProjectID)
	assert.True(t, info.RuntimeGeminiOAuth, "RuntimeGeminiOAuth must be true for OAuth account")
	assert.Equal(t, 1, detectorCalls, "detector must be called exactly once")

	// Verify the project was cached into token_state.
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, accountView.Id).Error)
	cachedState, err := DecryptAccountPoolTokenState(stored.TokenState)
	require.NoError(t, err)
	assert.Equal(t, detectedProject, cachedState.ProjectID, "project id must be persisted in token_state")

	// Second selection: cache is populated, detector must NOT be called again.
	ReleaseAccountPoolRuntimeSelection(ctx)
	ctx2 := newAccountPoolRuntimeTestContext()
	info2 := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "AIzaSy-channel-key",
			UpstreamModelName: "gemini-2.5-pro",
		},
	}
	err = ApplyAccountPoolRuntimeSelection(ctx2, info2, nil)
	require.NoError(t, err)
	defer ReleaseAccountPoolRuntimeSelection(ctx2)

	assert.Equal(t, detectedProject, info2.RuntimeGeminiProjectID, "second selection must use cached project id")
	assert.Equal(t, 1, detectorCalls, "detector must NOT be called again when cache is present")
}

// TestApplyAccountPoolRuntimeSelection_VertexServiceAccountExcludesOAuthFlag verifies that a
// Gemini Vertex service-account credential with a CACHED valid token (so no mint is needed)
// sets the Vertex routing fields but leaves RuntimeGeminiOAuth/RuntimeGeminiOAuthType cleared,
// keeping the two routing modes mutually exclusive (the cached access token would otherwise make
// accountPoolHasOAuthRuntimeCredential return true).
func TestApplyAccountPoolRuntimeSelection_VertexServiceAccountExcludesOAuthFlag(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	saJSON := `{"type":"service_account","project_id":"vertex-proj","client_email":"sa@vertex-proj.iam.gserviceaccount.com","token_uri":"https://oauth2.googleapis.com/token","private_key":"-----BEGIN PRIVATE KEY-----\nFAKE\n-----END PRIVATE KEY-----\n"}`
	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "vertex-sa-cached",
		Credential: AccountPoolCredentialConfig{
			Type:               AccountPoolCredentialTypeServiceAccount,
			ServiceAccountJSON: saJSON,
			Location:           "us-central1",
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "ya29.vertex-cached-token", // valid cache → mint (which needs the key) is skipped
			ExpiresAt:   9999999999,
		},
	})

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "AIzaSy-channel-key",
			UpstreamModelName: "gemini-2.5-pro",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)
	require.NoError(t, err)
	defer ReleaseAccountPoolRuntimeSelection(ctx)

	assert.True(t, info.RuntimeVertexServiceAccount, "vertex SA flag must be set")
	assert.Equal(t, "vertex-proj", info.RuntimeVertexProjectID)
	assert.Equal(t, "us-central1", info.RuntimeVertexLocation)
	assert.False(t, info.RuntimeGeminiOAuth, "RuntimeGeminiOAuth must be false for a service-account credential")
	assert.Equal(t, "", info.RuntimeGeminiOAuthType, "no cloudcode-pa oauth type for a service-account credential")
	assert.Equal(t, "ya29.vertex-cached-token", info.ApiKey, "cached minted token is carried as ApiKey")
}

// TestApplyAccountPoolRuntimeSelection_AntigravityProjectDetectedAndTyped verifies that
// an antigravity OAuth account goes through the SAME cloudcode-pa project detection as
// code_assist, and that info.RuntimeGeminiOAuthType is set to "antigravity" (the actual
// type), so the relay adaptor can apply antigravity-specific request wrapping.
func TestApplyAccountPoolRuntimeSelection_AntigravityProjectDetectedAndTyped(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:      "antigravity-no-project",
		OAuthType: AccountPoolGeminiOAuthTypeAntigravity,
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "ya29.antigravity-token",
			ExpiresAt:   9999999999,
		},
	})

	const detectedProject = "projects/antigravity-project-456"
	detectorCalls := 0
	origDetector := accountPoolDetectGeminiCodeAssistProject
	accountPoolDetectGeminiCodeAssistProject = func(_ context.Context, token, _ string) (string, error) {
		detectorCalls++
		assert.Equal(t, "ya29.antigravity-token", token, "detector must receive the runtime credential")
		return detectedProject, nil
	}
	t.Cleanup(func() { accountPoolDetectGeminiCodeAssistProject = origDetector })

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "AIzaSy-channel-key",
			UpstreamModelName: "gemini-2.5-pro",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)
	require.NoError(t, err)
	defer ReleaseAccountPoolRuntimeSelection(ctx)

	assert.Equal(t, AccountPoolGeminiOAuthTypeAntigravity, info.RuntimeGeminiOAuthType,
		"RuntimeGeminiOAuthType must be 'antigravity' (the actual type), not 'code_assist'")
	assert.Equal(t, detectedProject, info.RuntimeGeminiProjectID)
	assert.True(t, info.RuntimeGeminiOAuth, "RuntimeGeminiOAuth must be true for OAuth account")
	assert.Equal(t, 1, detectorCalls, "antigravity must reuse the code_assist detector exactly once")
}

// TestApplyAccountPoolRuntimeSelection_CodeAssistDetectionFailureReturnsError verifies
// that when the detector returns an error, ApplyAccountPoolRuntimeSelection propagates
// the error so the outer pool loop can sideline this account and retry another one.
func TestApplyAccountPoolRuntimeSelection_CodeAssistDetectionFailureReturnsError(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:      "code-assist-detect-fail",
		OAuthType: AccountPoolGeminiOAuthTypeCodeAssist,
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "ya29.fail-token",
			ExpiresAt:   9999999999,
		},
	})

	origDetector := accountPoolDetectGeminiCodeAssistProject
	accountPoolDetectGeminiCodeAssistProject = func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("cloudcode-pa.googleapis.com: connection refused")
	}
	t.Cleanup(func() { accountPoolDetectGeminiCodeAssistProject = origDetector })

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "AIzaSy-channel-key",
			UpstreamModelName: "gemini-2.5-pro",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)
	require.Error(t, err, "detection failure must propagate as an error so the pool loop can sideline the account")
	assert.Contains(t, err.Error(), "project detection")
}

// TestApplyAccountPoolRuntimeSelection_CodeAssistDetectionFailureLeavesOAuthTypeEmpty
// verifies FIX 4: when the project detector returns an error,
// ApplyAccountPoolRuntimeSelection must propagate the error AND leave
// info.RuntimeGeminiOAuthType at its reset value (""), not "code_assist".
// This prevents downstream code from treating the account as code_assist when
// project detection actually failed.
func TestApplyAccountPoolRuntimeSelection_CodeAssistDetectionFailureLeavesOAuthTypeEmpty(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name:      "code-assist-detect-fail-oauthtype",
		OAuthType: AccountPoolGeminiOAuthTypeCodeAssist,
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "ya29.fail-oauthtype-token",
			ExpiresAt:   9999999999,
		},
	})

	origDetector := accountPoolDetectGeminiCodeAssistProject
	accountPoolDetectGeminiCodeAssistProject = func(_ context.Context, _, _ string) (string, error) {
		return "", errors.New("network error during project detection")
	}
	t.Cleanup(func() { accountPoolDetectGeminiCodeAssistProject = origDetector })

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "AIzaSy-channel-key",
			UpstreamModelName: "gemini-2.5-pro",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)
	require.Error(t, err, "detection failure must propagate as error")
	assert.Empty(t, info.RuntimeGeminiOAuthType,
		"RuntimeGeminiOAuthType must be empty (not 'code_assist') when detection fails")
	assert.Empty(t, info.RuntimeGeminiProjectID,
		"RuntimeGeminiProjectID must be empty when detection fails")
}

// TestApplyAccountPoolRuntimeSelection_PlainGeminiOAuthNotCodeAssist verifies that a
// Gemini OAuth account WITHOUT oauth_type=code_assist does NOT set RuntimeGeminiOAuthType
// and does NOT call the Code Assist detector.
func TestApplyAccountPoolRuntimeSelection_PlainGeminiOAuthNotCodeAssist(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPoolWithPlatform(t, svc, model.AccountPoolPlatformGemini)
	channel := createAccountPoolServiceTestChannelWithType(t, constant.ChannelTypeGemini, common.ChannelStatusManuallyDisabled)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{
		Name: "gemini-standard-oauth",
		Credential: AccountPoolCredentialConfig{
			Type: AccountPoolCredentialTypeOAuth,
			// OAuthType is deliberately empty (or could be "ai_studio").
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "ya29.standard-gemini-token",
			ExpiresAt:   9999999999,
		},
	})

	detectorCalled := false
	origDetector := accountPoolDetectGeminiCodeAssistProject
	accountPoolDetectGeminiCodeAssistProject = func(_ context.Context, _, _ string) (string, error) {
		detectorCalled = true
		return "", errors.New("detector must not be called for non-code-assist accounts")
	}
	t.Cleanup(func() { accountPoolDetectGeminiCodeAssistProject = origDetector })

	ctx := newAccountPoolRuntimeTestContext()
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-2.5-pro",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId:         channel.Id,
			ApiKey:            "AIzaSy-channel-key",
			UpstreamModelName: "gemini-2.5-pro",
		},
	}

	err := ApplyAccountPoolRuntimeSelection(ctx, info, nil)
	require.NoError(t, err)
	defer ReleaseAccountPoolRuntimeSelection(ctx)

	assert.False(t, detectorCalled, "Code Assist detector must NOT be called for non-code-assist OAuth accounts")
	assert.Empty(t, info.RuntimeGeminiOAuthType, "RuntimeGeminiOAuthType must be empty for standard Gemini OAuth")
	assert.Empty(t, info.RuntimeGeminiProjectID, "RuntimeGeminiProjectID must be empty for standard Gemini OAuth")
	assert.True(t, info.RuntimeGeminiOAuth, "RuntimeGeminiOAuth must still be true for any Gemini OAuth account")
}
