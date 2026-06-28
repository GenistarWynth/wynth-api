package service

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeAffinityTestCandidate builds a minimal accountPoolAccountCandidate for unit tests
// that only care about account ID matching (no DB, no pool filters, no encryption).
func makeAffinityTestCandidate(id int) accountPoolAccountCandidate {
	return accountPoolAccountCandidate{
		account: model.AccountPoolAccount{Id: id},
	}
}

// TestAccountPoolAffinityRememberLookupForget covers the basic remember/lookup/forget contract.
func TestAccountPoolAffinityRememberLookupForget(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k1"
	const bindingID = 1
	const accountID = 42
	const t0 = int64(1000)

	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t0)

	t.Run("lookup returns the remembered account before expiry", func(t *testing.T) {
		got, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t0+1)
		require.True(t, ok)
		assert.Equal(t, accountID, got)
	})

	t.Run("lookup rejects a wrong bindingID", func(t *testing.T) {
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID+1, t0+1)
		assert.False(t, ok)
	})

	t.Run("forget removes the entry", func(t *testing.T) {
		forgetAccountPoolRuntimeAffinity(key)
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t0+1)
		assert.False(t, ok)
	})
}

// TestAccountPoolAffinityIdleTTLExpiry verifies that the sliding idle TTL causes expiry
// when no refresh happens.
func TestAccountPoolAffinityIdleTTLExpiry(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k2"
	const bindingID = 2
	const accountID = 7
	// Use a large non-zero t0 so the "now <= 0" real-clock branch in remember/lookup
	// is never triggered. Values must stay well below t0+hardCap to avoid the hard-cap path.
	const t0 = int64(1_000_000)

	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t0)

	t.Run("lookup succeeds one second before TTL boundary", func(t *testing.T) {
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t0+accountPoolRuntimeAffinityTTLSeconds-1)
		assert.True(t, ok)
	})

	t.Run("lookup fails at TTL boundary (expiresAt == now is expired)", func(t *testing.T) {
		// expiresAt = t0 + TTL; condition is expiresAt <= now, so now == expiresAt → expired.
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t0+accountPoolRuntimeAffinityTTLSeconds)
		assert.False(t, ok)
	})
}

// TestAccountPoolAffinityDigestKey verifies that accountPoolRuntimeAffinityDigest produces a
// deterministic hex string.
func TestAccountPoolAffinityDigestKey(t *testing.T) {
	d1 := accountPoolRuntimeAffinityDigest("hello")
	d2 := accountPoolRuntimeAffinityDigest("hello")
	d3 := accountPoolRuntimeAffinityDigest("world")

	assert.Equal(t, d1, d2)
	assert.NotEqual(t, d1, d3)
	assert.Len(t, d1, 64, "SHA-256 hex digest must be 64 chars")
}

// TestAccountPoolAffinityCreatedAtPreservedAcrossRefreshes asserts that the birth time
// (createdAt) is fixed on the first remember and NOT overwritten by subsequent remember calls,
// while expiresAt slides forward on each refresh.
func TestAccountPoolAffinityCreatedAtPreservedAcrossRefreshes(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k-birth"
	const bindingID = 5
	const accountID = 99
	// Non-zero so the now<=0 -> wall-clock fallback in remember() never fires;
	// otherwise createdAt would be the real clock and the assertions below would
	// not deterministically exercise the hard-cap branch.
	const t0 = int64(1_000_000)

	// First remember – establishes createdAt = t0.
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t0)

	// Refresh at t1 = one second before idle TTL would expire. expiresAt slides forward.
	t1 := t0 + accountPoolRuntimeAffinityTTLSeconds - 1
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t1)

	t.Run("refresh extends the idle window", func(t *testing.T) {
		// t1+1 is past the original expiresAt (t0+TTL) but within the refreshed window (t1+TTL).
		got, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, t1+1)
		require.True(t, ok)
		assert.Equal(t, accountID, got)
	})

	t.Run("hard cap cuts off even a freshly refreshed entry", func(t *testing.T) {
		// Refresh again right before the hard-cap boundary so the idle window is
		// genuinely still open at lookup time (expiresAt is in the future). The hard
		// cap — relative to the preserved createdAt=t0, which the refresh does NOT
		// reset — must still reject the pin, proving the hard cap (not idle-TTL) fires.
		tNear := t0 + accountPoolRuntimeAffinityHardCapSeconds - 1
		rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, tNear)
		hardCapBoundary := t0 + accountPoolRuntimeAffinityHardCapSeconds
		got, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, hardCapBoundary)
		assert.False(t, ok, "hard cap must reject the pin even when expiresAt was freshly refreshed")
		assert.Zero(t, got)
	})
}

// TestAccountPoolAffinityHardCapExpiry covers the hard TTL cap contract end-to-end:
// a session that keeps refreshing its pin must still be evicted after 4h.
func TestAccountPoolAffinityHardCapExpiry(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k-hardcap"
	const bindingID = 3
	const accountID = 55
	const t0 = int64(1_000_000)

	// Establish pin at t0.
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, t0)

	// Refresh just before hard cap boundary – simulating a long-lived active session.
	refreshTime := t0 + accountPoolRuntimeAffinityHardCapSeconds - 1
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountID, refreshTime)

	hardCapBoundary := t0 + accountPoolRuntimeAffinityHardCapSeconds

	t.Run("pin valid one second before hard cap", func(t *testing.T) {
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, hardCapBoundary-1)
		assert.True(t, ok)
	})

	t.Run("pin evicted at hard cap boundary", func(t *testing.T) {
		_, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, hardCapBoundary)
		assert.False(t, ok)
	})
}

// TestAccountPoolAffinityTransientRetentionViaScheduler is a scheduler-level test that drives
// selectAccountPoolAffinityCandidate directly. It verifies that when the pinned account is
// absent from the current candidates slice (e.g., it is in AttemptedAccountIDs for this
// request), the pin is NOT forgotten — only the current selection falls through to a fallback,
// while the next request with the account back in candidates honors the original pin.
//
// Eviction is owned by: the relay failure path (ForgetSelectedAccountPoolRuntimeAffinity) +
// the idle TTL + the hard cap. A transient absence must NOT drop the pin.
func TestAccountPoolAffinityTransientRetentionViaScheduler(t *testing.T) {
	resetAccountPoolRuntimeAffinitiesForTest()

	const key = "k-transient"
	const bindingID = 10
	const now = int64(500)

	accountA := makeAffinityTestCandidate(101)
	accountB := makeAffinityTestCandidate(202)

	// Pin to account A.
	rememberAccountPoolRuntimeAffinity(key, bindingID, accountA.account.Id, now)

	t.Run("returns ok=false when pinned account absent from candidates", func(t *testing.T) {
		// Pass only B in candidates (A is absent, simulating it was attempted this request).
		_, ok := selectAccountPoolAffinityCandidate(key, bindingID, []accountPoolAccountCandidate{accountB}, now)
		assert.False(t, ok)
	})

	t.Run("pin is NOT forgotten after transient absence", func(t *testing.T) {
		got, ok := lookupAccountPoolRuntimeAffinity(key, bindingID, now)
		require.True(t, ok, "pin must still be present after transient candidate absence")
		assert.Equal(t, accountA.account.Id, got)
	})

	t.Run("returns A when A is back in candidates", func(t *testing.T) {
		got, ok := selectAccountPoolAffinityCandidate(key, bindingID, []accountPoolAccountCandidate{accountB, accountA}, now)
		require.True(t, ok)
		assert.Equal(t, accountA.account.Id, got.account.Id)
	})
}

// makeMinimalRelayInfo constructs the minimal RelayInfo needed for signal-path tests.
func makeMinimalRelayInfo() *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		OriginModelName: "claude-3-5-sonnet",
		UserId:          1,
		TokenId:         1,
	}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelId:         1,
		UpstreamModelName: "claude-3-5-sonnet",
	}
	return info
}

// makeClaudeRequestWithMetadataUserID returns a *dto.ClaudeRequest with metadata.user_id set.
func makeClaudeRequestWithMetadataUserID(userID string) *dto.ClaudeRequest {
	meta, _ := json.Marshal(map[string]string{"user_id": userID})
	return &dto.ClaudeRequest{
		Model:    "claude-3-5-sonnet",
		Metadata: json.RawMessage(meta),
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	}
}

// makeClaudeRequestWithConversation returns a *dto.ClaudeRequest with no metadata but given messages.
func makeClaudeRequestWithConversation(system string, messages []dto.ClaudeMessage) *dto.ClaudeRequest {
	req := &dto.ClaudeRequest{
		Model:    "claude-3-5-sonnet",
		Messages: messages,
	}
	if system != "" {
		req.System = system
	}
	return req
}

// TestClaudeAffinityMetadataUserID verifies that a non-empty metadata.user_id produces a stable,
// user-id-keyed signal and that different user IDs produce different signals.
func TestClaudeAffinityMetadataUserID(t *testing.T) {
	info := makeMinimalRelayInfo()

	req1a := makeClaudeRequestWithMetadataUserID("user-abc")
	req1b := makeClaudeRequestWithMetadataUserID("user-abc")
	req2 := makeClaudeRequestWithMetadataUserID("user-xyz")

	sig1a := accountPoolRuntimeAffinitySignal(nil, info, req1a)
	sig1b := accountPoolRuntimeAffinitySignal(nil, info, req1b)
	sig2 := accountPoolRuntimeAffinitySignal(nil, info, req2)

	require.NotEmpty(t, sig1a, "metadata user_id should produce a non-empty signal")
	assert.Equal(t, sig1a, sig1b, "same user_id must produce the same signal")
	assert.NotEqual(t, sig1a, sig2, "different user_id must produce different signals")
	assert.Contains(t, sig1a, "claude_metadata_user:", "signal must carry the claude_metadata_user: prefix")
}

// TestClaudeAffinityConversationDigest verifies that two requests with identical system+messages
// produce the same digest signal, and that changed messages produce a different digest.
func TestClaudeAffinityConversationDigest(t *testing.T) {
	info := makeMinimalRelayInfo()

	msgs := []dto.ClaudeMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}
	req1 := makeClaudeRequestWithConversation("You are helpful.", msgs)
	req2 := makeClaudeRequestWithConversation("You are helpful.", msgs)
	req3 := makeClaudeRequestWithConversation("You are helpful.", []dto.ClaudeMessage{
		{Role: "user", Content: "different message"},
	})

	sig1 := accountPoolRuntimeAffinitySignal(nil, info, req1)
	sig2 := accountPoolRuntimeAffinitySignal(nil, info, req2)
	sig3 := accountPoolRuntimeAffinitySignal(nil, info, req3)

	require.NotEmpty(t, sig1, "non-empty conversation should produce a signal")
	assert.Equal(t, sig1, sig2, "identical conversation must produce the same digest signal")
	assert.NotEqual(t, sig1, sig3, "different messages must produce different digest signals")
	assert.Contains(t, sig1, "claude_digest:", "signal must carry the claude_digest: prefix")
}

// TestClaudeAffinityEmptyRequest verifies that an empty ClaudeRequest yields no affinity signal.
func TestClaudeAffinityEmptyRequest(t *testing.T) {
	info := makeMinimalRelayInfo()
	req := &dto.ClaudeRequest{Model: "claude-3-5-sonnet"}

	sig := accountPoolRuntimeAffinitySignal(nil, info, req)
	assert.Empty(t, sig, "an empty Claude request must produce no affinity signal")
}

// TestClaudeAffinityHeaderTakesPrecedence verifies that a session header signal overrides
// the Claude metadata-based signal (header-first contract).
func TestClaudeAffinityHeaderTakesPrecedence(t *testing.T) {
	info := makeMinimalRelayInfo()
	if info.RequestHeaders == nil {
		info.RequestHeaders = make(map[string]string)
	}
	info.RequestHeaders["X-Session-Id"] = "ses-override-123"

	req := makeClaudeRequestWithMetadataUserID("user-abc")
	sig := accountPoolRuntimeAffinitySignal(nil, info, req)

	require.NotEmpty(t, sig)
	assert.Contains(t, sig, "header:", "session header must take precedence over Claude metadata signal")
	assert.Contains(t, sig, "ses-override-123")
}

// TestClaudeAffinityMetadataUserIDLong verifies that a long/opaque user_id is digested
// (signal must still be non-empty and stable across identical values).
func TestClaudeAffinityMetadataUserIDLong(t *testing.T) {
	info := makeMinimalRelayInfo()
	longUID := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6"

	req1 := makeClaudeRequestWithMetadataUserID(longUID)
	req2 := makeClaudeRequestWithMetadataUserID(longUID)

	sig1 := accountPoolRuntimeAffinitySignal(nil, info, req1)
	sig2 := accountPoolRuntimeAffinitySignal(nil, info, req2)

	require.NotEmpty(t, sig1)
	assert.Equal(t, sig1, sig2, "long user_id must still produce a stable, identical signal")
}

// makeGeminiRequest builds a *dto.GeminiChatRequest with the given system instruction text,
// and contents (role+text pairs). Pass an empty systemText to omit SystemInstructions.
func makeGeminiRequest(systemText string, contents []struct{ role, text string }) *dto.GeminiChatRequest {
	req := &dto.GeminiChatRequest{}
	if systemText != "" {
		req.SystemInstructions = &dto.GeminiChatContent{
			Parts: []dto.GeminiPart{{Text: systemText}},
		}
	}
	for _, c := range contents {
		req.Contents = append(req.Contents, dto.GeminiChatContent{
			Role:  c.role,
			Parts: []dto.GeminiPart{{Text: c.text}},
		})
	}
	return req
}

// TestGeminiAffinityConversationDigest verifies that two GeminiChatRequests with identical
// system+contents produce the same digest signal, and that changed contents produce a different
// signal. This is the primary contract for Gemini session affinity.
func TestGeminiAffinityConversationDigest(t *testing.T) {
	info := makeMinimalRelayInfo()

	contents := []struct{ role, text string }{
		{"user", "hello gemini"},
		{"model", "hi there"},
	}
	req1 := makeGeminiRequest("You are a Gemini assistant.", contents)
	req2 := makeGeminiRequest("You are a Gemini assistant.", contents)
	req3 := makeGeminiRequest("You are a Gemini assistant.", []struct{ role, text string }{
		{"user", "completely different message"},
	})

	sig1 := accountPoolRuntimeAffinitySignal(nil, info, req1)
	sig2 := accountPoolRuntimeAffinitySignal(nil, info, req2)
	sig3 := accountPoolRuntimeAffinitySignal(nil, info, req3)

	require.NotEmpty(t, sig1, "non-empty Gemini conversation should produce a signal")
	assert.Equal(t, sig1, sig2, "identical Gemini conversation must produce the same digest signal")
	assert.NotEqual(t, sig1, sig3, "different Gemini contents must produce different digest signals")
	assert.Contains(t, sig1, "gemini_digest:", "signal must carry the gemini_digest: prefix")
}

// TestGeminiAffinityEmptyRequest verifies that a GeminiChatRequest with no system and no
// contents yields no affinity signal.
func TestGeminiAffinityEmptyRequest(t *testing.T) {
	info := makeMinimalRelayInfo()
	req := &dto.GeminiChatRequest{}

	sig := accountPoolRuntimeAffinitySignal(nil, info, req)
	assert.Empty(t, sig, "an empty GeminiChatRequest must produce no affinity signal")
}

// TestGeminiAffinityHeaderTakesPrecedence verifies that a session header signal overrides
// the Gemini content-digest signal (header-first contract).
func TestGeminiAffinityHeaderTakesPrecedence(t *testing.T) {
	info := makeMinimalRelayInfo()
	if info.RequestHeaders == nil {
		info.RequestHeaders = make(map[string]string)
	}
	info.RequestHeaders["X-Session-Id"] = "ses-gemini-override"

	req := makeGeminiRequest("system", []struct{ role, text string }{{"user", "hi"}})
	sig := accountPoolRuntimeAffinitySignal(nil, info, req)

	require.NotEmpty(t, sig)
	assert.Contains(t, sig, "header:", "session header must take precedence over Gemini digest signal")
	assert.Contains(t, sig, "ses-gemini-override")
}

// TestGeminiAffinityNonTextPartsIgnored verifies that non-text parts (inline_data,
// function_call) are silently skipped and only text parts contribute to the digest.
func TestGeminiAffinityNonTextPartsIgnored(t *testing.T) {
	info := makeMinimalRelayInfo()

	// Build two requests with the same text but one has an extra inline_data part.
	textOnlyReq := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{{Text: "describe this image"}}},
		},
	}
	mixedReq := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{Role: "user", Parts: []dto.GeminiPart{
				{Text: "describe this image"},
				{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: "abc123"}},
			}},
		},
	}

	sigText := accountPoolRuntimeAffinitySignal(nil, info, textOnlyReq)
	sigMixed := accountPoolRuntimeAffinitySignal(nil, info, mixedReq)

	require.NotEmpty(t, sigText)
	assert.Equal(t, sigText, sigMixed, "non-text parts must not affect the affinity digest")
}

// TestOpenAIAffinityUnchangedByClaudeAddition is a regression guard: adding the Claude case
// must not alter any OpenAI Responses signals.
func TestOpenAIAffinityUnchangedByClaudeAddition(t *testing.T) {
	info := makeMinimalRelayInfo()

	t.Run("OpenAIResponsesRequest PreviousResponseID", func(t *testing.T) {
		req := &dto.OpenAIResponsesRequest{PreviousResponseID: "resp-abc"}
		sig := accountPoolRuntimeAffinitySignal(nil, info, req)
		assert.Equal(t, "responses_previous:resp-abc", sig)
	})

	t.Run("OpenAIResponsesCompactionRequest PreviousResponseID", func(t *testing.T) {
		req := &dto.OpenAIResponsesCompactionRequest{PreviousResponseID: "resp-xyz"}
		sig := accountPoolRuntimeAffinitySignal(nil, info, req)
		assert.Equal(t, "responses_compaction_previous:resp-xyz", sig)
	})

	t.Run("unknown request type yields empty", func(t *testing.T) {
		sig := accountPoolRuntimeAffinitySignal(nil, info, nil)
		assert.Empty(t, sig)
	})
}
