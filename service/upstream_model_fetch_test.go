package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFetchChannelUpstreamModelIDsOpenAICompatible(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := newUpstreamModelFetchTestServer(t)
	defer server.Close()

	channel := &model.Channel{
		Type:    constant.ChannelTypeOpenAI,
		Key:     "sk-test",
		BaseURL: common.GetPointer(server.URL),
	}

	ids, err := FetchChannelUpstreamModelIDs(channel)

	require.NoError(t, err)
	assert.Equal(t, []string{"gpt-4o", "gpt-4o-mini"}, ids)
}

func TestBuildFetchModelsHeadersAppliesOverridesAndSkipsPassthroughRules(t *testing.T) {
	overrideData, err := common.Marshal(map[string]any{
		"X-Upstream-Key": "token {api_key}",
		"re:^x-client-":  "ignored",
	})
	require.NoError(t, err)
	channel := &model.Channel{
		Type:           constant.ChannelTypeOpenAI,
		HeaderOverride: common.GetPointer(string(overrideData)),
	}

	headers, err := BuildFetchModelsHeaders(channel, "sk-test")

	require.NoError(t, err)
	assert.Equal(t, "Bearer sk-test", headers.Get("Authorization"))
	assert.Equal(t, "token sk-test", headers.Get("X-Upstream-Key"))
	assert.Empty(t, headers.Get("re:^x-client-"))
}

func newUpstreamModelFetchTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))

		writeUpstreamModelFetchTestJSON(t, w, map[string]any{
			"data": []map[string]string{
				{"id": "gpt-4o"},
				{"id": " gpt-4o-mini "},
				{"id": ""},
			},
		})
	}))
}

func writeUpstreamModelFetchTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	data, err := common.Marshal(value)
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
}
