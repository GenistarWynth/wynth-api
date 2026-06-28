package grokweb

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFlattenMessagesRoleOrderAndPrefixes(t *testing.T) {
	msgs := []dto.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hi"},
		{Role: "assistant", Content: "Hello!"},
		{Role: "user", Content: "What is 2+2?"},
	}
	got := flattenMessages(msgs)
	want := "System: You are helpful.\n\n" +
		"Human: Hi\n\n" +
		"Assistant: Hello!\n\n" +
		"Human: What is 2+2?"
	assert.Equal(t, want, got)
}

func TestFlattenMessagesSkipsEmptyContent(t *testing.T) {
	msgs := []dto.Message{
		{Role: "system", Content: ""},
		{Role: "user", Content: "only this"},
	}
	assert.Equal(t, "Human: only this", flattenMessages(msgs))
}

func TestBuildGrokRequestShape(t *testing.T) {
	req := &dto.GeneralOpenAIRequest{
		Model: "grok-4.3",
		Messages: []dto.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
		},
	}
	body := buildGrokRequest(req, modelToModeID(req.Model))

	assert.Equal(t, "System: sys\n\nHuman: hello", body.Message)
	assert.Equal(t, "grok-420-computer-use-sa", body.ModeID)
	// flags / overrides for the text MVP
	assert.True(t, body.Temporary)
	assert.True(t, body.DisableMemory)
	assert.True(t, body.SendFinalMetadata)
	assert.False(t, body.DisableSearch)
	assert.False(t, body.EnableImageGeneration)
	assert.False(t, body.IsAsyncChat)
	// all tool overrides must be false
	assert.Equal(t, grokToolOverrides{}, body.ToolOverrides)
	// non-nil empty slices (so they marshal as [] not null)
	require.NotNil(t, body.CollectionIDs)
	require.NotNil(t, body.FileAttachments)
}

func TestModelToModeID(t *testing.T) {
	cases := map[string]string{
		"grok-4.3":      "grok-420-computer-use-sa",
		"grok-4.3-beta": "grok-420-computer-use-sa",
		"grok-fast":     "fast",
		"grok-expert":   "expert",
		"grok-heavy":    "heavy",
		"grok-auto":     "auto",
		"unknown-model": defaultModeID, // sane fallback
	}
	for model, want := range cases {
		assert.Equalf(t, want, modelToModeID(model), "model=%s", model)
	}
}

func TestBuildSSOCookie(t *testing.T) {
	// bare token, no cf_clearance
	assert.Equal(t, "sso=abc; sso-rw=abc", buildSSOCookie("abc", ""))
	// "sso=" prefix is stripped
	assert.Equal(t, "sso=abc; sso-rw=abc", buildSSOCookie("sso=abc", ""))
	// cf_clearance appended when present
	assert.Equal(t, "sso=abc; sso-rw=abc; cf_clearance=cfv", buildSSOCookie("abc", "cfv"))
}

func TestApplyGrokHeaders(t *testing.T) {
	h := http.Header{}
	applyGrokHeaders(&h, "tok", "cfv")

	assert.Equal(t, staticStatsigID, h.Get("x-statsig-id"))
	assert.Equal(t, defaultUserAgent, h.Get("User-Agent"))
	assert.Equal(t, "https://grok.com", h.Get("Origin"))
	assert.Equal(t, "https://grok.com/", h.Get("Referer"))
	assert.Equal(t, "application/json", h.Get("Content-Type"))
	assert.Equal(t, "sso=tok; sso-rw=tok; cf_clearance=cfv", h.Get("Cookie"))
	// a uuid request id is generated (non-empty)
	assert.NotEmpty(t, h.Get("x-xai-request-id"))
}

func TestParseCredential(t *testing.T) {
	sso, cf := parseCredential("plain-token")
	assert.Equal(t, "plain-token", sso)
	assert.Equal(t, "", cf)

	sso, cf = parseCredential(`{"sso":"jtok","cf_clearance":"jcf"}`)
	assert.Equal(t, "jtok", sso)
	assert.Equal(t, "jcf", cf)
}
