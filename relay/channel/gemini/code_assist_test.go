package gemini

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withCodeAssistBaseURL temporarily overrides the package base URL for the
// duration of t, restoring the original afterwards.
func withCodeAssistBaseURL(t *testing.T, url string) {
	t.Helper()
	orig := geminiCodeAssistBaseURL
	geminiCodeAssistBaseURL = url
	t.Cleanup(func() { geminiCodeAssistBaseURL = orig })
}

func newCodeAssistTestContext(t *testing.T, method, body string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, "/v1/chat/completions", strings.NewReader(body))
	return c
}

func codeAssistInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RuntimeGeminiOAuth:     true,
		RuntimeGeminiOAuthType: service.AccountPoolGeminiOAuthTypeCodeAssist,
		RuntimeGeminiProjectID: "projects/my-gcp-project",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "ya29.code-assist-token",
			UpstreamModelName: "gemini-2.5-pro",
			ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
		},
	}
}

func standardGeminiInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RuntimeGeminiOAuth: false,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "AIzaSy-api-key",
			UpstreamModelName: "gemini-2.5-pro",
			ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
		},
	}
}

// --- Request wrap -----------------------------------------------------------

func TestWrapGeminiCodeAssistRequestShape(t *testing.T) {
	standard := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}],"generationConfig":{"temperature":0}}`

	wrapped, err := wrapGeminiCodeAssistRequest([]byte(standard), "projects/my-gcp-project", "gemini-2.5-pro")
	require.NoError(t, err)

	var got geminiCodeAssistRequest
	require.NoError(t, common.Unmarshal(wrapped, &got))

	assert.Equal(t, "projects/my-gcp-project", got.Project, "project must be top-level")
	assert.Equal(t, "gemini-2.5-pro", got.Model, "model must be top-level")
	assert.JSONEq(t, standard, string(got.Request),
		"the original standard request must be nested verbatim under request")
}

// TestDoRequestWrapsBodyForCodeAssist asserts the actual bytes that hit the wire
// are the cloudcode-pa wrapper when the account is code_assist.
func TestDoRequestWrapsBodyForCodeAssist(t *testing.T) {
	service.InitHttpClient()

	standard := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`

	var captured []byte
	var capturedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path + "?" + r.URL.RawQuery
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"response":{"candidates":[]}}`))
	}))
	t.Cleanup(srv.Close)
	withCodeAssistBaseURL(t, srv.URL)

	c := newCodeAssistTestContext(t, http.MethodPost, standard)
	info := codeAssistInfo()
	a := &Adaptor{}

	resp, err := a.DoRequest(c, info, strings.NewReader(standard))
	require.NoError(t, err)
	httpResp, ok := resp.(*http.Response)
	require.True(t, ok)
	_ = httpResp.Body.Close()

	assert.Equal(t, "/v1internal:generateContent?", capturedPath,
		"code_assist non-stream must route to /v1internal:generateContent")

	var wrapper geminiCodeAssistRequest
	require.NoError(t, common.Unmarshal(captured, &wrapper),
		"upstream body must be a code-assist wrapper")
	assert.Equal(t, "projects/my-gcp-project", wrapper.Project)
	assert.Equal(t, "gemini-2.5-pro", wrapper.Model)
	assert.JSONEq(t, standard, string(wrapper.Request))
}

// TestDoRequestPassesBodyThroughForStandard is the zero-regression assertion for
// the request path: a non-code_assist account must send the raw body unchanged
// with no wrapper.
func TestDoRequestPassesBodyThroughForStandard(t *testing.T) {
	service.InitHttpClient()

	standard := `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`

	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"candidates":[]}`))
	}))
	t.Cleanup(srv.Close)

	c := newCodeAssistTestContext(t, http.MethodPost, standard)
	info := standardGeminiInfo()
	info.ChannelMeta.ChannelBaseUrl = srv.URL
	a := &Adaptor{}

	resp, err := a.DoRequest(c, info, strings.NewReader(standard))
	require.NoError(t, err)
	httpResp, ok := resp.(*http.Response)
	require.True(t, ok)
	_ = httpResp.Body.Close()

	assert.JSONEq(t, standard, string(captured),
		"standard path must send the body byte-equivalent, no wrapper")
	assert.NotContains(t, string(captured), `"request"`,
		"standard path must not introduce a code-assist wrapper")
}

// --- GetRequestURL ----------------------------------------------------------

func TestGetRequestURLCodeAssist(t *testing.T) {
	withCodeAssistBaseURL(t, "https://cloudcode-pa.example")
	a := &Adaptor{}

	t.Run("non-stream", func(t *testing.T) {
		info := codeAssistInfo()
		info.IsStream = false
		url, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://cloudcode-pa.example/v1internal:generateContent", url)
		assert.False(t, info.DisablePing, "non-stream must not flip DisablePing")
	})

	t.Run("stream", func(t *testing.T) {
		info := codeAssistInfo()
		info.IsStream = true
		url, err := a.GetRequestURL(info)
		require.NoError(t, err)
		assert.Equal(t, "https://cloudcode-pa.example/v1internal:streamGenerateContent?alt=sse", url)
		assert.True(t, info.DisablePing, "stream must set DisablePing for parity")
	})
}

// TestGetRequestURLStandardUnchanged is the zero-regression assertion for the URL
// path: a non-code_assist account must produce the standard models/ URL and must
// NOT touch the cloudcode-pa base.
func TestGetRequestURLStandardUnchanged(t *testing.T) {
	withCodeAssistBaseURL(t, "https://cloudcode-pa.example")
	a := &Adaptor{}

	info := standardGeminiInfo()
	info.IsStream = false
	url, err := a.GetRequestURL(info)
	require.NoError(t, err)

	assert.Contains(t, url, "https://generativelanguage.googleapis.com")
	assert.Contains(t, url, "/models/gemini-2.5-pro:generateContent")
	assert.NotContains(t, url, "cloudcode-pa", "standard path must never route to cloudcode-pa")
	assert.NotContains(t, url, "v1internal", "standard path must never use v1internal")
}

// --- Non-stream response unwrap --------------------------------------------

func TestUnwrapGeminiCodeAssistResponse(t *testing.T) {
	inner := `{"candidates":[{"content":{"parts":[{"text":"hello"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"totalTokenCount":7},"modelVersion":"gemini-2.5-pro"}`

	t.Run("wrapped is unwrapped", func(t *testing.T) {
		wrapped := `{"response":` + inner + `,"responseId":"abc","modelVersion":"gemini-2.5-pro"}`
		out := unwrapGeminiCodeAssistResponse([]byte(wrapped))
		assert.JSONEq(t, inner, string(out),
			"the inner response object must be extracted verbatim")
	})

	t.Run("already unwrapped passes through", func(t *testing.T) {
		out := unwrapGeminiCodeAssistResponse([]byte(inner))
		assert.JSONEq(t, inner, string(out),
			"a body that is not a wrapper must pass through unchanged")
	})

	t.Run("empty response field passes through", func(t *testing.T) {
		body := `{"response":null,"responseId":"abc"}`
		out := unwrapGeminiCodeAssistResponse([]byte(body))
		assert.JSONEq(t, body, string(out),
			"a null inner response must not be extracted; fall back to original")
	})

	t.Run("non-json passes through", func(t *testing.T) {
		body := `not json at all`
		out := unwrapGeminiCodeAssistResponse([]byte(body))
		assert.Equal(t, body, string(out))
	})
}

// TestIsGeminiCodeAssistPredicate verifies the routing predicate that guards ALL
// code_assist-specific behaviour in DoRequest/DoResponse/GetRequestURL. A wrong
// predicate would silently unwrap standard responses or skip code_assist wrapping.
// Option (b): we rely on predicate correctness + the existing DoRequest and
// GetRequestURL integration tests for full regression; the response-path is
// guarded by the same predicate, so if it is correct the body is never touched
// for standard accounts.
func TestIsGeminiCodeAssistPredicate(t *testing.T) {
	assert.False(t, isGeminiCodeAssist(nil),
		"nil RelayInfo must be safe and return false")
	assert.False(t, isGeminiCodeAssist(standardGeminiInfo()),
		"standard (API-key) account must not be treated as code_assist")
	assert.True(t, isGeminiCodeAssist(codeAssistInfo()),
		"account with OAuthType=code_assist must be detected as code_assist")

	// Partial state: OAuth but wrong type must not match.
	partial := codeAssistInfo()
	partial.RuntimeGeminiOAuthType = "ai_studio"
	assert.False(t, isGeminiCodeAssist(partial),
		"OAuthType != code_assist must not be detected as code_assist")
}

// --- Stream unwrap reader ---------------------------------------------------

func TestGeminiCodeAssistStreamReader(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "unwraps response from each data event",
			in: "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]}}]}}\n" +
				"\n" +
				"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"b\"}]}}]}}\n" +
				"\n",
			want: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"a\"}]}}]}\n" +
				"\n" +
				"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"b\"}]}}]}\n" +
				"\n",
		},
		{
			name: "passthrough lines preserved verbatim",
			in: "event: message\n" +
				"data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"c\"}]}}]}}\n" +
				"\n" +
				": this is a comment\n" +
				"data: [DONE]\n",
			want: "event: message\n" +
				"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"c\"}]}}]}\n" +
				"\n" +
				": this is a comment\n" +
				"data: [DONE]\n",
		},
		{
			name: "non-wrapper data passes through unchanged",
			in:   "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"d\"}]}}]}\n",
			want: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"d\"}]}}]}\n",
		},
		{
			name: "non-json data passes through unchanged",
			in:   "data: garbage-not-json\n",
			want: "data: garbage-not-json\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newGeminiCodeAssistStreamReader(io.NopCloser(strings.NewReader(tc.in)))
			out, err := io.ReadAll(r)
			require.NoError(t, err)
			require.NoError(t, r.Close())
			assert.Equal(t, tc.want, string(out))
		})
	}
}

// TestGeminiCodeAssistStreamReaderPartialReads verifies the buffered reader is
// drained correctly across many tiny Read calls (the consumer may request 1
// byte at a time), so SSE content is not lost or duplicated.
func TestGeminiCodeAssistStreamReaderPartialReads(t *testing.T) {
	in := "data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"x\"}]}}]}}\n\n"
	want := "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"x\"}]}}]}\n\n"

	r := newGeminiCodeAssistStreamReader(io.NopCloser(strings.NewReader(in)))
	defer r.Close()

	var got []byte
	one := make([]byte, 1)
	for {
		n, err := r.Read(one)
		if n > 0 {
			got = append(got, one[:n]...)
		}
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	assert.Equal(t, want, string(got),
		"one-byte-at-a-time reads must reproduce the unwrapped stream exactly")
}

// TestGetRequestURLCodeAssistNormalizesThinkingModelName verifies FIX 1:
// when ThinkingAdapterEnabled is true and the upstream model name carries a
// thinking suffix, GetRequestURL must strip the suffix BEFORE the code_assist
// early-return so that DoRequest wraps the clean base model name into the
// cloudcode-pa envelope's "model" field.
func TestGetRequestURLCodeAssistNormalizesThinkingModelName(t *testing.T) {
	withCodeAssistBaseURL(t, "https://cloudcode-pa.example")

	// Enable thinking adapter for the duration of this test.
	settings := model_setting.GetGeminiSettings()
	origEnabled := settings.ThinkingAdapterEnabled
	settings.ThinkingAdapterEnabled = true
	t.Cleanup(func() { settings.ThinkingAdapterEnabled = origEnabled })

	a := &Adaptor{}

	tests := []struct {
		name          string
		upstreamModel string
		wantModel     string
	}{
		{"thinking suffix", "gemini-2.5-pro-thinking", "gemini-2.5-pro"},
		{"thinking-budget suffix", "gemini-2.5-pro-thinking-1024", "gemini-2.5-pro"},
		{"nothinking suffix", "gemini-2.5-pro-nothinking", "gemini-2.5-pro"},
		{"clean model unchanged", "gemini-2.5-pro", "gemini-2.5-pro"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := codeAssistInfo()
			info.UpstreamModelName = tc.upstreamModel
			// OriginModelName must also be set so ShouldPreserveThinkingSuffix works correctly.
			info.OriginModelName = tc.upstreamModel

			_, err := a.GetRequestURL(info)
			require.NoError(t, err)

			assert.Equal(t, tc.wantModel, info.UpstreamModelName,
				"UpstreamModelName (used by DoRequest to build the wrapper 'model' field) must be normalized before code_assist routing")
		})
	}
}

// TestGeminiCodeAssistStreamReaderLargeLine verifies FIX 3:
// a single SSE data line whose inner JSON payload exceeds the old 4 MiB cap
// (we use ~5 MiB) must be unwrapped without error.
func TestGeminiCodeAssistStreamReaderLargeLine(t *testing.T) {
	// Build a ~5 MiB inner payload by padding the text field.
	const targetSize = 5 * 1024 * 1024
	padding := strings.Repeat("x", targetSize)
	inner := `{"candidates":[{"content":{"parts":[{"text":"` + padding + `"}]}}]}`
	wrapped := `data: {"response":` + inner + `}` + "\n"

	r := newGeminiCodeAssistStreamReader(io.NopCloser(strings.NewReader(wrapped)))
	out, err := io.ReadAll(r)
	require.NoError(t, err, "a >4 MiB SSE line must not return bufio.ErrTooLong")
	require.NoError(t, r.Close())

	assert.True(t, strings.HasPrefix(string(out), "data: {"),
		"output must be a data: line with the inner JSON")
	assert.True(t, strings.Contains(string(out), `"candidates"`),
		"unwrapped inner JSON must contain the candidates key")
	assert.False(t, strings.Contains(string(out), `"response"`),
		"the outer response wrapper must have been stripped")
}
