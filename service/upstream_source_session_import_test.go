package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplyImportedSessionNewAPIAccessToken(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// probe: /user/self/groups must succeed with the imported token headers
		assert.Equal(t, "access-imported", r.Header.Get("Authorization"))
		assert.Equal(t, "9", r.Header.Get("New-Api-User"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	t.Cleanup(server.Close)

	source := &model.UpstreamSource{
		Type:             model.UpstreamSourceTypeNewAPI,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api",
		AuthConfig:       `{"email":"a@b.com","password":"p"}`,
	}
	require.NoError(t, model.DB.Create(source).Error)

	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "access-imported",
		UserID:      9,
	})
	require.NoError(t, err)

	got, err := parseNewAPIAuthConfig(source)
	require.NoError(t, err)
	assert.Equal(t, "access-imported", got.AccessToken)
	assert.Equal(t, 9, got.UserID)
	assert.Equal(t, "manual", got.SessionSource)
	// email/password must survive so credential rotation is not forced.
	assert.Equal(t, "a@b.com", got.Email)
	assert.Equal(t, "p", got.Password)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	persisted, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	var persistedCfg newAPIAuthConfig
	require.NoError(t, common.UnmarshalJsonStr(persisted, &persistedCfg))
	assert.Equal(t, "access-imported", persistedCfg.AccessToken, "the imported session must be persisted so discover/sync reuse it instead of logging in again")
	assert.Equal(t, 9, persistedCfg.UserID)
}

func TestApplyImportedSessionNewAPICookieExchange(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			cookie, err := r.Cookie("session")
			require.NoError(t, err)
			assert.Equal(t, "cookie-value", cookie.Value)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":42}}`))
		case "/api/user/token":
			cookie, err := r.Cookie("session")
			require.NoError(t, err)
			assert.Equal(t, "cookie-value", cookie.Value)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":"exchanged-token"}`))
		case "/api/user/self/groups":
			assert.Equal(t, "exchanged-token", r.Header.Get("Authorization"))
			assert.Equal(t, "42", r.Header.Get("New-Api-User"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	source := &model.UpstreamSource{
		Type:             model.UpstreamSourceTypeNewAPI,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api",
		AuthConfig:       `{"email":"a@b.com","password":"p"}`,
	}
	require.NoError(t, model.DB.Create(source).Error)

	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		SessionCookie: "session=cookie-value",
	})
	require.NoError(t, err)

	got, err := parseNewAPIAuthConfig(source)
	require.NoError(t, err)
	assert.Equal(t, "exchanged-token", got.AccessToken)
	assert.Equal(t, 42, got.UserID)
	assert.Equal(t, "manual", got.SessionSource)
}

func TestApplyImportedSessionSub2APIAccessToken(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			assert.Equal(t, "Bearer jwt-imported", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":[]}`))
		case "/api/v1/groups/rates":
			assert.Equal(t, "Bearer jwt-imported", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":{}}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)

	source := &model.UpstreamSource{
		Type:             model.UpstreamSourceTypeSub2API,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api/v1",
		AuthConfig:       `{"email":"a@b.com","password":"p"}`,
	}
	require.NoError(t, model.DB.Create(source).Error)

	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "jwt-imported",
	})
	require.NoError(t, err)

	got, err := parseSub2APIAuthConfig(source)
	require.NoError(t, err)
	assert.Equal(t, "jwt-imported", got.AccessToken)
	assert.Equal(t, "manual", got.SessionSource)
	assert.Greater(t, got.ExpiresAt, common.GetTimestamp())

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	persisted, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	var persistedCfg sub2APIAuthConfig
	require.NoError(t, common.UnmarshalJsonStr(persisted, &persistedCfg))
	assert.Equal(t, "jwt-imported", persistedCfg.AccessToken)
}

func TestApplyImportedSessionFailsValidationDoesNotPersist(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"invalid access token"}`))
	}))
	t.Cleanup(server.Close)

	originalAuth := `{"email":"a@b.com","password":"p"}`
	source := &model.UpstreamSource{
		Type:             model.UpstreamSourceTypeNewAPI,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api",
		AuthConfig:       originalAuth,
	}
	require.NoError(t, model.DB.Create(source).Error)

	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "bad-token",
		UserID:      9,
	})
	require.Error(t, err)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, originalAuth, reloaded.AuthConfig, "a session that fails the live probe must not be persisted")
}
