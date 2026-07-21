package service

import (
	"bytes"
	"context"
	"encoding/json"
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
		assert.IsType(t, float64(0), payload["group_id"])
		assert.Equal(t, float64(10), payload["group_id"])
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
		assert.IsType(t, float64(0), payload["group_id"])
		assert.Equal(t, float64(20), payload["group_id"])
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

func TestSub2APIAdapterUpdateKeyRetriesWithConfigVersion(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	putCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/keys/123", r.URL.Path)
		switch r.Method {
		case http.MethodPut:
			putCalls++
			var payload map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			if putCalls == 1 {
				assert.NotContains(t, payload, "config_version")
				w.WriteHeader(http.StatusBadRequest)
				writeSub2APITestJSON(t, w, map[string]any{"code": 400, "message": "config_version is required"})
				return
			}
			assert.Equal(t, float64(7), payload["config_version"])
			writeSub2APITestJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"id": 123}})
		case http.MethodGet:
			writeSub2APITestJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"id": 123, "config_version": 7}})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	key, err := (Sub2APIAdapter{Client: server.Client()}).UpdateKey(context.Background(), source, "123", "20", "updated channel")

	require.NoError(t, err)
	assert.Equal(t, "123", key.ID)
	assert.Equal(t, 2, putCalls)
}

func TestSub2APIAdapterUpdateKeyRetriesWithZeroConfigVersionWhenMissing(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	putCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/keys/123", r.URL.Path)
		switch r.Method {
		case http.MethodPut:
			putCalls++
			var payload map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			if putCalls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				writeSub2APITestJSON(t, w, map[string]any{"code": 400, "message": "CONFIG_VERSION is REQUIRED"})
				return
			}
			assert.Equal(t, float64(0), payload["config_version"])
			writeSub2APITestJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"id": 123}})
		case http.MethodGet:
			writeSub2APITestJSON(t, w, map[string]any{"code": 0, "data": map[string]any{"id": 123}})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	_, err := (Sub2APIAdapter{Client: server.Client()}).UpdateKey(context.Background(), source, "123", "20", "updated channel")

	require.NoError(t, err)
	assert.Equal(t, 2, putCalls)
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

// TestSub2APICodeIndicatesError is a regression guard for gateways that send
// the envelope "code" field as a numeric string (or omit it) instead of a
// JSON number. Only a nonzero number, in either encoding, should be treated
// as an upstream error; HTTP status is the primary success signal.
func TestSub2APICodeIndicatesError(t *testing.T) {
	tests := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{"string zero", json.RawMessage(`"0"`), false},
		{"int zero", json.RawMessage(`0`), false},
		{"absent", nil, false},
		{"empty string literal", json.RawMessage(`""`), false},
		{"success string", json.RawMessage(`"success"`), false},
		{"string nonzero", json.RawMessage(`"5"`), true},
		{"int nonzero", json.RawMessage(`5`), true},
		{"string large error code", json.RawMessage(`"40001"`), true},
		{"string non-integer numeric code", json.RawMessage(`"5.0"`), true},
		{"string non-integer zero code", json.RawMessage(`"0.0"`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sub2APICodeIndicatesError(tt.raw))
		})
	}
}

// TestDiscoverGroupsSucceedsWithStringCode reproduces the reported decode bug:
// some sub2api gateways send the envelope "code" field as a STRING ("0")
// rather than a JSON number, and the previous hardcoded `int` field made
// common.Unmarshal fail with "cannot unmarshal string into Go struct field".
func TestDiscoverGroupsSucceedsWithStringCode(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"code":"0","message":"ok","data":[{"id":1,"name":"g","platform":"openai","status":"enabled"}]}`))
			require.NoError(t, err)
		case "/api/v1/groups/rates":
			w.Header().Set("Content-Type", "application/json")
			_, err := w.Write([]byte(`{"code":"0","data":{}}`))
			require.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: server.Client()}

	groups, err := adapter.DiscoverGroups(context.Background(), source)

	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "1", groups[0].ID)
	assert.Equal(t, "g", groups[0].Name)
}

// TestEnsureAccessTokenRefreshesWithRefreshToken verifies that an
// expired-but-refreshable access token is renewed via the refresh-token
// exchange BEFORE falling back to password login, and that the refreshed
// tokens are persisted onto source.AuthConfig.
func TestEnsureAccessTokenRefreshesWithRefreshToken(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	var loginCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			var payload map[string]string
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			assert.Equal(t, "rt-1", payload["refresh_token"])
			writeSub2APITestJSON(t, w, map[string]any{
				"code": "0",
				"data": map[string]any{
					"access_token":  "new-at",
					"refresh_token": "rt-2",
					"expires_in":    3600,
				},
			})
		case "/api/v1/auth/login":
			loginCalled = true
			writeSub2APITestJSON(t, w, map[string]any{
				"code": 0,
				"data": map[string]any{"access_token": "from-login", "expires_in": 3600},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, map[string]any{
		"access_token":  "stale",
		"refresh_token": "rt-1",
		"expires_at":    1,
	})
	adapter := Sub2APIAdapter{Client: server.Client()}

	token, err := adapter.ensureAccessToken(context.Background(), source)

	require.NoError(t, err)
	assert.Equal(t, "new-at", token)
	assert.False(t, loginCalled, "password login must not be called when refresh-token renewal succeeds")

	var auth map[string]any
	require.NoError(t, common.UnmarshalJsonStr(source.AuthConfig, &auth))
	assert.Equal(t, "new-at", auth["access_token"])
	assert.Equal(t, "rt-2", auth["refresh_token"])
}

// TestEnsureAccessTokenZeroExpiryIsNever verifies that an access token with
// expires_at == 0 (meaning "never expires", e.g. resolved from a JWT without
// an exp claim) is used as-is without any HTTP call.
func TestEnsureAccessTokenZeroExpiryIsNever(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	source := newSub2APITestSource(t, "https://example.com", map[string]any{
		"access_token": "at",
		"expires_at":   0,
	})
	adapter := Sub2APIAdapter{Client: &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("unexpected HTTP call to %s", req.URL.String())
		}),
	}}

	token, err := adapter.ensureAccessToken(context.Background(), source)

	require.NoError(t, err)
	assert.Equal(t, "at", token)
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

func TestSub2APIAdapterAllowsPrivateURLWhenSourceAllowsPrivateIP(t *testing.T) {
	withSub2APIFetchSetting(t, false)

	source := newSub2APITestSource(t, "http://127.0.0.1:8080", validTokenAuthConfig())
	source.SyncConfig = `{"allow_private_ip":true}`
	adapter := Sub2APIAdapter{Client: &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/v1/groups/available":
				return sub2APITestResponse(t, map[string]any{
					"code":    0,
					"message": "success",
					"data":    []map[string]any{},
				}), nil
			case "/api/v1/groups/rates":
				return sub2APITestResponse(t, map[string]any{
					"code":    0,
					"message": "success",
					"data":    map[string]float64{},
				}), nil
			default:
				return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
			}
		}),
	}}

	groups, err := adapter.DiscoverGroups(context.Background(), source)

	require.NoError(t, err)
	assert.Empty(t, groups)
}

func TestSub2APIAdapterAllowsPrivateURLWithNumericPrivateIPFlag(t *testing.T) {
	withSub2APIFetchSetting(t, false)

	source := newSub2APITestSource(t, "http://127.0.0.1:8080", validTokenAuthConfig())
	source.SyncConfig = `{"allow_private_ip":1}`
	adapter := Sub2APIAdapter{Client: &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/v1/groups/available":
				return sub2APITestResponse(t, map[string]any{
					"code":    0,
					"message": "success",
					"data":    []map[string]any{},
				}), nil
			case "/api/v1/groups/rates":
				return sub2APITestResponse(t, map[string]any{
					"code":    0,
					"message": "success",
					"data":    map[string]float64{},
				}), nil
			default:
				return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
			}
		}),
	}}

	groups, err := adapter.DiscoverGroups(context.Background(), source)

	require.NoError(t, err)
	assert.Empty(t, groups)
}

func TestSub2APIAdapterClientValidatesRedirects(t *testing.T) {
	withSub2APIFetchSetting(t, false)

	delegated := false
	transport := &http.Transport{}
	baseClient := &http.Client{
		Transport: transport,
		Timeout:   123 * time.Millisecond,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			delegated = true
			return nil
		},
	}
	source := newSub2APITestSource(t, "https://example.com", validTokenAuthConfig())
	wrapped := sub2APIHTTPClient(&Sub2APIAdapter{Client: baseClient}, source)
	require.NotNil(t, wrapped.CheckRedirect)
	assert.Same(t, transport, wrapped.Transport)
	assert.Equal(t, baseClient.Timeout, wrapped.Timeout)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/private", nil)
	err := wrapped.CheckRedirect(req, nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked")
	assert.False(t, delegated)
}

func TestSub2APIAdapterClientAllowsPrivateRedirectWhenSourceAllowsPrivateIP(t *testing.T) {
	withSub2APIFetchSetting(t, false)

	delegated := false
	baseClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			delegated = true
			return nil
		},
	}
	source := newSub2APITestSource(t, "https://example.com", validTokenAuthConfig())
	source.SyncConfig = `{"allow_private_ip":true}`
	wrapped := sub2APIHTTPClient(&Sub2APIAdapter{Client: baseClient}, source)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/private", nil)
	err := wrapped.CheckRedirect(req, nil)

	require.NoError(t, err)
	assert.True(t, delegated)
}

func TestSub2APIAdapterDefaultRedirectValidationSkipsGlobalPrivateIPBlockWhenSourceAllowsPrivateIP(t *testing.T) {
	withSub2APIFetchSetting(t, false)

	globalRedirectCalled := false
	baseClient := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			globalRedirectCalled = true
			return errors.New("global private IP block")
		},
	}
	source := newSub2APITestSource(t, "https://example.com", validTokenAuthConfig())
	source.SyncConfig = `{"allow_private_ip":true}`
	wrapped := sub2APIClientWithRedirectValidation(baseClient, source, false)

	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/private", nil)
	err := wrapped.CheckRedirect(req, nil)

	require.NoError(t, err)
	assert.False(t, globalRedirectCalled)
}

func TestSub2APIAdapterSanitizesAndCapsErrors(t *testing.T) {
	rawKey := "sk-" + strings.Repeat("a", 32)
	err := fmt.Errorf(`GET https://example.com/path?access_token=query-access&refresh_token=query-refresh&api_key=query-key&password=query-password&token=query-token failed Authorization: Bearer bearer-secret Cookie: session-secret X-API-Key: header-key password=body-password refresh_token=body-refresh api_key=body-key {"access_token":"json-access","refresh_token":"json-refresh","api_key":"json-key","password":"json-password","token":"json-token"} generated key %s %s`, rawKey, strings.Repeat("x", 2000))

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
	assert.NotContains(t, sanitized, rawKey)
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

func TestSub2APIAdapterDiscoverGroupsSurfacesStatusOnEmptyBody(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	// Expired session: the upstream replies 401 with an EMPTY body. Previously
	// this surfaced as "decode upstream response failed: unexpected end of JSON
	// input"; it must now report the real HTTP status so the admin knows to
	// re-import the session.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)

	source := newSub2APITestSource(t, server.URL, validTokenAuthConfig())
	adapter := Sub2APIAdapter{Client: server.Client()}

	_, err := adapter.DiscoverGroups(context.Background(), source)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 401")
	assert.NotContains(t, err.Error(), "unexpected end of JSON input")
}
