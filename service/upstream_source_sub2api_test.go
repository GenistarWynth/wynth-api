package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSub2APIAdapterLoginUpdatesAuthConfigWithBearerTokens(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	var sawBearer bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			var payload map[string]string
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			assert.Equal(t, "admin@example.com", payload["email"])
			assert.Equal(t, "password-secret", payload["password"])
			writeSub2APITestJSON(t, w, map[string]any{
				"code":    0,
				"message": "success",
				"data": map[string]any{
					"access_token":  "access-token",
					"refresh_token": "refresh-token",
					"expires_in":    3600,
				},
			})
		case "/api/v1/groups/available":
			sawBearer = r.Header.Get("Authorization") == "Bearer access-token"
			writeSub2APITestJSON(t, w, map[string]any{"code": 0, "message": "success", "data": []any{}})
		case "/api/v1/groups/rates":
			writeSub2APITestJSON(t, w, map[string]any{"code": 0, "message": "success", "data": map[string]float64{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, map[string]any{
		"email":    "admin@example.com",
		"password": "password-secret",
	})
	adapter := Sub2APIAdapter{Client: server.Client()}

	groups, err := adapter.DiscoverGroups(context.Background(), source)

	require.NoError(t, err)
	assert.Empty(t, groups)
	assert.True(t, sawBearer)

	var auth map[string]any
	require.NoError(t, common.UnmarshalJsonStr(source.AuthConfig, &auth))
	assert.Equal(t, "access-token", auth["access_token"])
	assert.Equal(t, "refresh-token", auth["refresh_token"])
	expiresAt, ok := auth["expires_at"].(float64)
	require.True(t, ok)
	assert.Greater(t, int64(expiresAt), time.Now().Unix())
}

func TestSub2APIAdapterLoginReports2FARequired(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/auth/login", r.URL.Path)
		writeSub2APITestJSON(t, w, map[string]any{
			"code":    0,
			"message": "success",
			"data":    map[string]any{"requires_2fa": true},
		})
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, map[string]any{
		"email":    "admin@example.com",
		"password": "password-secret",
	})
	adapter := Sub2APIAdapter{Client: server.Client()}

	_, err := adapter.DiscoverGroups(context.Background(), source)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUpstreamSource2FARequired))
}

func TestSub2APIAdapterDiscoverGroupsUsesUserRates(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer existing-token", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/v1/groups/available":
			writeSub2APITestJSON(t, w, map[string]any{
				"code":    0,
				"message": "success",
				"data": []map[string]any{
					{"id": 10, "name": "cheap", "platform": "openai", "status": "enabled", "rate_multiplier": 1.5},
					{"id": 20, "name": "custom", "platform": "claude", "status": "enabled", "rate_multiplier": 2.0},
					{"id": nil, "name": "missing id", "platform": "gemini", "status": "disabled"},
				},
			})
		case "/api/v1/groups/rates":
			writeSub2APITestJSON(t, w, map[string]any{
				"code":    0,
				"message": "success",
				"data":    map[string]float64{"20": 0.25},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: server.Client()}

	groups, err := adapter.DiscoverGroups(context.Background(), source)

	require.NoError(t, err)
	require.Len(t, groups, 3)
	assert.Equal(t, "10", groups[0].ID)
	assert.Equal(t, "cheap", groups[0].Name)
	assert.Equal(t, "openai", groups[0].Platform)
	require.NotNil(t, groups[0].RateMultiplier)
	assert.Equal(t, 1.5, *groups[0].RateMultiplier)
	require.NotNil(t, groups[0].EffectiveRateMultiplier)
	assert.Equal(t, 1.5, *groups[0].EffectiveRateMultiplier)

	assert.Equal(t, "20", groups[1].ID)
	require.NotNil(t, groups[1].RateMultiplier)
	assert.Equal(t, 2.0, *groups[1].RateMultiplier)
	require.NotNil(t, groups[1].EffectiveRateMultiplier)
	assert.Equal(t, 0.25, *groups[1].EffectiveRateMultiplier)

	assert.Empty(t, groups[2].ID)
	assert.Nil(t, groups[2].RateMultiplier)
	assert.Nil(t, groups[2].EffectiveRateMultiplier)
}

func TestSub2APIAdapterCreateKeyReturnsIDAndFullKey(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/keys", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		assert.Equal(t, "10", fmt.Sprint(payload["group_id"]))
		assert.Equal(t, "generated channel", payload["name"])
		writeSub2APITestJSON(t, w, map[string]any{
			"code":    0,
			"message": "success",
			"data":    map[string]any{"id": 123, "key": "sk-full-generated", "name": "generated channel", "group_id": 10},
		})
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: server.Client()}

	key, err := adapter.CreateKey(context.Background(), source, "10", "generated channel")

	require.NoError(t, err)
	assert.Equal(t, "123", key.ID)
	assert.Equal(t, "sk-full-generated", key.Key)
	assert.Equal(t, "generated channel", key.Name)
	assert.Equal(t, "10", key.GroupID)
}

func TestSub2APIAdapterAddsDefaultRequestDeadline(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	adapter := Sub2APIAdapter{Client: &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			deadline, ok := req.Context().Deadline()
			if !ok {
				return nil, errors.New("request context missing deadline")
			}
			if time.Until(deadline) <= 0 {
				return nil, errors.New("request context deadline already expired")
			}
			return sub2APITestResponse(t, map[string]any{
				"code":    0,
				"message": "success",
				"data":    map[string]any{"id": 123, "key": "sk-generated", "name": "generated channel", "group_id": 10},
			}), nil
		}),
	}}
	source := newSub2APITestSource(t, "https://example.com", validTokenAuthConfig())

	key, err := adapter.CreateKey(context.Background(), source, "10", "generated channel")

	require.NoError(t, err)
	assert.Equal(t, "123", key.ID)
}

func TestSub2APIAdapterPreservesCallerRequestDeadline(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	expectedDeadline := time.Now().Add(250 * time.Millisecond)
	adapter := Sub2APIAdapter{Client: &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			deadline, ok := req.Context().Deadline()
			if !ok {
				return nil, errors.New("request context missing deadline")
			}
			if !deadline.Equal(expectedDeadline) {
				return nil, fmt.Errorf("deadline changed: got %s want %s", deadline, expectedDeadline)
			}
			return sub2APITestResponse(t, map[string]any{
				"code":    0,
				"message": "success",
				"data":    map[string]any{"id": 123, "key": "sk-generated", "name": "generated channel", "group_id": 10},
			}), nil
		}),
	}}
	source := newSub2APITestSource(t, "https://example.com", validTokenAuthConfig())
	ctx, cancel := context.WithDeadline(context.Background(), expectedDeadline)
	defer cancel()

	key, err := adapter.CreateKey(ctx, source, "10", "generated channel")

	require.NoError(t, err)
	assert.Equal(t, "123", key.ID)
}

func TestSub2APIAdapterUpdateKeySendsPutAndNormalizesResponse(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/keys/123", r.URL.Path)
		require.Equal(t, http.MethodPut, r.Method)
		require.Equal(t, "Bearer existing-token", r.Header.Get("Authorization"))
		var payload map[string]any
		require.NoError(t, common.DecodeJson(r.Body, &payload))
		assert.Equal(t, "20", fmt.Sprint(payload["group_id"]))
		assert.Equal(t, "updated channel", payload["name"])
		writeSub2APITestJSON(t, w, map[string]any{
			"code":    0,
			"message": "success",
			"data":    map[string]any{"id": 123, "name": "updated channel", "group_id": 20},
		})
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: server.Client()}

	key, err := adapter.UpdateKey(context.Background(), source, "123", "20", "updated channel")

	require.NoError(t, err)
	assert.Equal(t, "123", key.ID)
	assert.Empty(t, key.Key)
	assert.Equal(t, "updated channel", key.Name)
	assert.Equal(t, "20", key.GroupID)
}

func TestSub2APIAdapterListKeysReadsPaginatedItems(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	var pages []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/keys", r.URL.Path)
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "10", r.URL.Query().Get("group_id"))
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		switch page {
		case "1":
			writeSub2APITestJSON(t, w, map[string]any{
				"code":    0,
				"message": "success",
				"data": map[string]any{
					"items":     []map[string]any{{"id": 1, "key": "sk-one", "name": "one", "group_id": 10}, {"id": 2, "key": "sk-two", "name": "two", "group_id": 10}},
					"total":     3,
					"page":      1,
					"page_size": 2,
				},
			})
		case "2":
			writeSub2APITestJSON(t, w, map[string]any{
				"code":    0,
				"message": "success",
				"data": map[string]any{
					"items":     []map[string]any{{"id": 3, "key": "sk-three", "name": "three", "group_id": 10}},
					"total":     3,
					"page":      2,
					"page_size": 2,
				},
			})
		default:
			t.Fatalf("unexpected page %q", page)
		}
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: server.Client()}

	keys, err := adapter.ListKeys(context.Background(), source, "10")

	require.NoError(t, err)
	require.Len(t, keys, 3)
	assert.Equal(t, []string{"1", "2"}, pages)
	assert.Equal(t, "1", keys[0].ID)
	assert.Equal(t, "sk-one", keys[0].Key)
	assert.Equal(t, "3", keys[2].ID)
	assert.Equal(t, "sk-three", keys[2].Key)
}

func TestSub2APIAdapterNonZeroEnvelopeCodeIsError(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			writeSub2APITestJSON(t, w, map[string]any{
				"code":    401,
				"message": "invalid token Bearer should-not-leak",
				"data":    nil,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: server.Client()}

	_, err := adapter.DiscoverGroups(context.Background(), source)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "upstream error")
	assert.Contains(t, err.Error(), "401")
	assert.NotContains(t, err.Error(), "should-not-leak")
}

func TestSub2APIAdapterRejectsOversizedResponseBody(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/keys", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		w.Header().Set("Content-Type", "application/json")
		_, err := w.Write([]byte(`{"code":0,"message":"success","data":{"id":1,"key":"sk-test","name":"` + strings.Repeat("x", 2*1024*1024) + `","group_id":10}}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: server.Client()}

	_, err := adapter.CreateKey(context.Background(), source, "10", "oversized")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "response body too large")
}

func TestSub2APIAdapterRejectsBlockedURL(t *testing.T) {
	withSub2APIFetchSetting(t, false)

	source := newSub2APITestSource(t, "http://127.0.0.1:8080", validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: http.DefaultClient}

	_, err := adapter.DiscoverGroups(context.Background(), source)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "request reject")
}

func TestSub2APIAdapterSanitizesAndCapsErrors(t *testing.T) {
	err := fmt.Errorf(`GET https://example.com/path?access_token=query-access&refresh_token=query-refresh&api_key=query-key&password=query-password&token=query-token failed Authorization: Bearer bearer-secret Cookie: session-secret X-API-Key: header-key password=body-password refresh_token=body-refresh api_key=body-key {"access_token":"json-access","refresh_token":"json-refresh","api_key":"json-key","password":"json-password","token":"json-token"} %s`, strings.Repeat("x", 2000))

	sanitized := SanitizeUpstreamSourceError(err)

	assert.LessOrEqual(t, len(sanitized), 1024)
	assert.NotContains(t, sanitized, "query-access")
	assert.NotContains(t, sanitized, "query-refresh")
	assert.NotContains(t, sanitized, "query-key")
	assert.NotContains(t, sanitized, "query-password")
	assert.NotContains(t, sanitized, "query-token")
	assert.NotContains(t, sanitized, "bearer-secret")
	assert.NotContains(t, sanitized, "session-secret")
	assert.NotContains(t, sanitized, "header-key")
	assert.NotContains(t, sanitized, "body-password")
	assert.NotContains(t, sanitized, "body-refresh")
	assert.NotContains(t, sanitized, "body-key")
	assert.NotContains(t, sanitized, "json-access")
	assert.NotContains(t, sanitized, "json-refresh")
	assert.NotContains(t, sanitized, "json-key")
	assert.NotContains(t, sanitized, "json-password")
	assert.NotContains(t, sanitized, "json-token")
}

func writeSub2APITestJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	data, err := common.Marshal(payload)
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func sub2APITestResponse(t *testing.T, payload any) *http.Response {
	t.Helper()
	data, err := common.Marshal(payload)
	require.NoError(t, err)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(data)),
	}
}

func newSub2APITestSource(t *testing.T, baseURL string, authConfig map[string]any) *model.UpstreamSource {
	t.Helper()
	data, err := common.Marshal(authConfig)
	require.NoError(t, err)
	return &model.UpstreamSource{
		Type:             model.UpstreamSourceTypeSub2API,
		BaseURL:          baseURL,
		AdminAPIBasePath: "/api/v1",
		AuthConfig:       string(data),
	}
}

func validTokenAuthConfig() map[string]any {
	return map[string]any{
		"email":        "admin@example.com",
		"password":     "password-secret",
		"access_token": "existing-token",
		"expires_at":   time.Now().Add(time.Hour).Unix(),
	}
}

func withSub2APIFetchSetting(t *testing.T, allowPrivate bool) {
	t.Helper()
	fetchSetting := system_setting.GetFetchSetting()
	old := *fetchSetting
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = allowPrivate
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = []string{}
	fetchSetting.ApplyIPFilterForDomain = false
	t.Cleanup(func() {
		*fetchSetting = old
	})
}

func TestFormatNullableIntID(t *testing.T) {
	var nilID *int64
	assert.Empty(t, formatNullableIntID(nilID))

	id := int64(123)
	assert.Equal(t, strconv.FormatInt(id, 10), formatNullableIntID(&id))
}
