package service

import (
	"context"
	stdjson "encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAPIAdapterDiscoverGroupsUsesDashboardGroupsAndCachesAccessToken(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	var sawGeneratedTokenAuth bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/login":
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]string
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			assert.Equal(t, "admin@example.com", payload["username"])
			assert.Equal(t, "password-secret", payload["password"])
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "login-session"})
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"id": 42,
				},
			})
		case "/api/user/token":
			require.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "42", r.Header.Get("New-Api-User"))
			require.NotNil(t, findNewAPITestCookie(r, "session"))
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data":    "management-token",
			})
		case "/api/user/self/groups":
			require.Equal(t, http.MethodGet, r.Method)
			sawGeneratedTokenAuth = r.Header.Get("Authorization") == "management-token" &&
				r.Header.Get("New-Api-User") == "42"
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"ChatGPT-Pro": map[string]any{
						"ratio": 0.1,
						"desc":  "OpenAI Pro lane",
					},
					"Claude": map[string]any{
						"ratio": 0.2,
						"desc":  "Anthropic fallback",
					},
					"auto": map[string]any{
						"ratio": "自动",
						"desc":  "auto",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newNewAPITestSource(t, server.URL, map[string]any{
		"email":    "admin@example.com",
		"password": "password-secret",
	})
	adapter := NewAPIAdapter{Client: server.Client()}

	groups, err := adapter.DiscoverGroups(context.Background(), source)

	require.NoError(t, err)
	require.Len(t, groups, 2)
	assert.True(t, sawGeneratedTokenAuth)
	assert.Equal(t, "ChatGPT-Pro", groups[0].ID)
	assert.Equal(t, "ChatGPT-Pro", groups[0].Name)
	assert.Equal(t, "OpenAI Pro lane", groups[0].Description)
	assert.Equal(t, "openai", groups[0].Platform)
	require.NotNil(t, groups[0].EffectiveRateMultiplier)
	assert.Equal(t, 0.1, *groups[0].EffectiveRateMultiplier)
	assert.Equal(t, "Claude", groups[1].ID)
	assert.Equal(t, "anthropic", groups[1].Platform)

	var auth map[string]any
	require.NoError(t, common.UnmarshalJsonStr(source.AuthConfig, &auth))
	assert.Equal(t, "management-token", auth["access_token"])
	assert.Equal(t, float64(42), auth["user_id"])
}

func TestNewAPIAdapterCreateKeyCreatesTokenAndReadsFullKey(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	var created bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "existing-management-token", r.Header.Get("Authorization"))
		require.Equal(t, "7", r.Header.Get("New-Api-User"))
		switch r.URL.Path {
		case "/api/token/":
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			assert.Equal(t, "generated channel", payload["name"])
			assert.Equal(t, "pro", payload["group"])
			assert.Equal(t, true, payload["unlimited_quota"])
			assert.Equal(t, float64(-1), payload["expired_time"])
			created = true
			writeNewAPITestJSON(t, w, map[string]any{"success": true, "message": ""})
		case "/api/token/search":
			require.True(t, created)
			require.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "generated channel", r.URL.Query().Get("keyword"))
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"id":              123,
							"name":            "generated channel",
							"group":           "pro",
							"key":             "sk-****masked",
							"expired_time":    -1,
							"unlimited_quota": true,
							"status":          1,
						},
					},
					"total":     1,
					"page":      1,
					"page_size": 100,
				},
			})
		case "/api/token/123/key":
			require.Equal(t, http.MethodPost, r.Method)
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"key": "sk-full-generated",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newNewAPITestSource(t, server.URL, map[string]any{
		"access_token": "existing-management-token",
		"user_id":      7,
	})
	adapter := NewAPIAdapter{Client: server.Client()}

	key, err := adapter.CreateKey(context.Background(), source, "pro", "generated channel")

	require.NoError(t, err)
	assert.Equal(t, "123", key.ID)
	assert.Equal(t, "sk-full-generated", key.Key)
	assert.Equal(t, "generated channel", key.Name)
	assert.Equal(t, "pro", key.GroupID)
}

func TestNewAPIAdapterCreateKeyUsesNewestExactNameAndGroupMatch(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	var keyRequestPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "existing-management-token", r.Header.Get("Authorization"))
		require.Equal(t, "7", r.Header.Get("New-Api-User"))
		switch r.URL.Path {
		case "/api/token/":
			require.Equal(t, http.MethodPost, r.Method)
			writeNewAPITestJSON(t, w, map[string]any{"success": true, "message": ""})
		case "/api/token/search":
			require.Equal(t, http.MethodGet, r.Method)
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"items": []map[string]any{
						{"id": 10, "name": "generated channel", "group": "pro"},
						{"id": 20, "name": "generated channel", "group": "pro"},
						{"id": 30, "name": "generated channel", "group": "default"},
					},
					"total":     3,
					"page":      1,
					"page_size": 100,
				},
			})
		case "/api/token/20/key":
			keyRequestPath = r.URL.Path
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data":    map[string]any{"key": "sk-newest-generated"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newNewAPITestSource(t, server.URL, map[string]any{
		"access_token": "existing-management-token",
		"user_id":      7,
	})
	adapter := NewAPIAdapter{Client: server.Client()}

	key, err := adapter.CreateKey(context.Background(), source, "pro", "generated channel")

	require.NoError(t, err)
	assert.Equal(t, "20", key.ID)
	assert.Equal(t, "sk-newest-generated", key.Key)
	assert.Equal(t, "/api/token/20/key", keyRequestPath)
}

func TestNewAPIAdapterUpdateKeyUpdatesTokenAndReadsFullKey(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	var updated bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "existing-management-token", r.Header.Get("Authorization"))
		require.Equal(t, "7", r.Header.Get("New-Api-User"))
		switch r.URL.Path {
		case "/api/token/123":
			require.Equal(t, http.MethodGet, r.Method)
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"id":              123,
					"name":            "old channel",
					"group":           "default",
					"expired_time":    -1,
					"unlimited_quota": true,
				},
			})
		case "/api/token/":
			require.Equal(t, http.MethodPut, r.Method)
			var payload map[string]any
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			assert.Equal(t, float64(123), payload["id"])
			assert.Equal(t, "updated channel", payload["name"])
			assert.Equal(t, "pro", payload["group"])
			assert.Equal(t, float64(1), payload["status"])
			updated = true
			writeNewAPITestJSON(t, w, map[string]any{"success": true, "message": ""})
		case "/api/token/123/key":
			require.True(t, updated)
			require.Equal(t, http.MethodPost, r.Method)
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data":    map[string]any{"key": "sk-updated"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newNewAPITestSource(t, server.URL, map[string]any{
		"access_token": "existing-management-token",
		"user_id":      7,
	})
	adapter := NewAPIAdapter{Client: server.Client()}

	key, err := adapter.UpdateKey(context.Background(), source, "123", "pro", "updated channel")

	require.NoError(t, err)
	assert.Equal(t, "123", key.ID)
	assert.Equal(t, "sk-updated", key.Key)
	assert.Equal(t, "updated channel", key.Name)
	assert.Equal(t, "pro", key.GroupID)
}

func TestNewAPIAdapterRefreshesExpiredCachedAccessToken(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	var loginCount int
	var groupRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self/groups":
			groupRequests++
			require.Equal(t, "42", r.Header.Get("New-Api-User"))
			switch r.Header.Get("Authorization") {
			case "stale-token":
				writeNewAPITestJSON(t, w, map[string]any{
					"success": false,
					"message": "Unauthorized, invalid access token",
				})
			case "fresh-token":
				writeNewAPITestJSON(t, w, map[string]any{
					"success": true,
					"message": "",
					"data": map[string]any{
						"ChatGPT": map[string]any{
							"ratio": 0.01,
							"desc":  "OpenAI",
						},
					},
				})
			default:
				t.Fatalf("unexpected Authorization header %q", r.Header.Get("Authorization"))
			}
		case "/api/user/login":
			loginCount++
			require.Equal(t, http.MethodPost, r.Method)
			var payload map[string]string
			require.NoError(t, common.DecodeJson(r.Body, &payload))
			assert.Equal(t, "admin@example.com", payload["username"])
			assert.Equal(t, "password-secret", payload["password"])
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "login-session"})
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data":    map[string]any{"id": 42},
			})
		case "/api/user/token":
			require.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "42", r.Header.Get("New-Api-User"))
			require.NotNil(t, findNewAPITestCookie(r, "session"))
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data":    "fresh-token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newNewAPITestSource(t, server.URL, map[string]any{
		"email":        "admin@example.com",
		"password":     "password-secret",
		"access_token": "stale-token",
		"user_id":      42,
	})
	adapter := NewAPIAdapter{Client: server.Client()}

	groups, err := adapter.DiscoverGroups(context.Background(), source)

	require.NoError(t, err)
	require.Len(t, groups, 1)
	assert.Equal(t, "ChatGPT", groups[0].ID)
	assert.Equal(t, 1, loginCount)
	assert.Equal(t, 2, groupRequests)

	var auth map[string]any
	require.NoError(t, common.UnmarshalJsonStr(source.AuthConfig, &auth))
	assert.Equal(t, "fresh-token", auth["access_token"])
}

func TestNewAPIAdapterListKeysFiltersByGroupAndReadsFullKeys(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	keyRequests := make([]string, 0)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "existing-management-token", r.Header.Get("Authorization"))
		require.Equal(t, "7", r.Header.Get("New-Api-User"))
		switch r.URL.Path {
		case "/api/token/search":
			require.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "", r.URL.Query().Get("keyword"))
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data": map[string]any{
					"items": []map[string]any{
						{"id": 1, "name": "one", "group": "pro"},
						{"id": 2, "name": "two", "group": "default"},
						{"id": 3, "name": "three", "group": "pro"},
					},
					"total":     3,
					"page":      1,
					"page_size": 100,
				},
			})
		case "/api/token/1/key", "/api/token/3/key":
			keyRequests = append(keyRequests, r.URL.Path)
			writeNewAPITestJSON(t, w, map[string]any{
				"success": true,
				"message": "",
				"data":    map[string]any{"key": "sk-" + r.URL.Path[len("/api/token/"):len(r.URL.Path)-len("/key")]},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	source := newNewAPITestSource(t, server.URL, map[string]any{
		"access_token": "existing-management-token",
		"user_id":      7,
	})
	adapter := NewAPIAdapter{Client: server.Client()}

	keys, err := adapter.ListKeys(context.Background(), source, "pro")

	require.NoError(t, err)
	require.Len(t, keys, 2)
	assert.Equal(t, "1", keys[0].ID)
	assert.Equal(t, "sk-1", keys[0].Key)
	assert.Equal(t, "3", keys[1].ID)
	assert.Equal(t, "sk-3", keys[1].Key)
	assert.Equal(t, []string{"/api/token/1/key", "/api/token/3/key"}, keyRequests)
}

// TestNewAPIAdapterReusesPersistedSession is a regression guard for the
// existing in-memory reuse contract: a second discover on the same source
// must NOT log in again once access_token+user_id are cached. Task 2 must
// keep this passing while routing AuthConfig reads through the decrypt
// helper and persisting refreshed sessions.
func TestNewAPIAdapterReusesPersistedSession(t *testing.T) {
	withSub2APIFetchSetting(t, true)

	loginCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/user/login"):
			loginCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42}}`))
		case strings.HasSuffix(r.URL.Path, "/user/token"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":"access-xyz"}`))
		case strings.HasSuffix(r.URL.Path, "/user/self/groups"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	source := &model.UpstreamSource{
		Type:             model.UpstreamSourceTypeNewAPI,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api",
		AuthConfig:       `{"email":"a@b.com","password":"p"}`,
	}
	adapter := NewAPIAdapter{Client: server.Client()}

	// First discover triggers exactly one login and populates the cached token.
	_, err := adapter.DiscoverGroups(context.Background(), source)
	require.NoError(t, err)
	assert.Equal(t, 1, loginCalls)

	// A second discover on the same in-memory source (token now cached) must NOT log in again.
	_, err = adapter.DiscoverGroups(context.Background(), source)
	require.NoError(t, err)
	assert.Equal(t, 1, loginCalls, "cached access_token+user_id should short-circuit login")
}

func TestDefaultUpstreamSourceAdapterFactorySupportsNewAPI(t *testing.T) {
	adapter, err := DefaultUpstreamSourceAdapterFactory(model.UpstreamSourceTypeNewAPI)

	require.NoError(t, err)
	assert.IsType(t, NewAPIAdapter{}, adapter)
}

func TestParseNewAPIGroupRatioAcceptsJSONNumber(t *testing.T) {
	ratio, ok := parseNewAPIGroupRatio(stdjson.Number("0.125"))

	require.True(t, ok)
	assert.Equal(t, 0.125, ratio)
}

func writeNewAPITestJSON(t *testing.T, w http.ResponseWriter, payload any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	data, err := common.Marshal(payload)
	require.NoError(t, err)
	_, err = w.Write(data)
	require.NoError(t, err)
}

func findNewAPITestCookie(r *http.Request, name string) *http.Cookie {
	for _, cookie := range r.Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}

func newNewAPITestSource(t *testing.T, baseURL string, authConfig map[string]any) *model.UpstreamSource {
	t.Helper()
	data, err := common.Marshal(authConfig)
	require.NoError(t, err)
	return &model.UpstreamSource{
		Type:             model.UpstreamSourceTypeNewAPI,
		BaseURL:          baseURL,
		AdminAPIBasePath: "/api",
		RelayBaseURL:     baseURL,
		AuthConfig:       string(data),
	}
}
