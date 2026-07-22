package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverAuthFailurePersistsExpiredSanitizedHealth(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"invalid access_token=super-secret-session"}`))
	}))
	t.Cleanup(server.Close)

	source := model.UpstreamSource{
		Name:             "expired-source",
		Type:             model.UpstreamSourceTypeNewAPI,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api",
		RelayBaseURL:     server.URL,
		AuthConfig:       `{"access_token":"super-secret-session","user_id":9}`,
	}
	require.NoError(t, model.DB.Create(&source).Error)

	_, err := (&UpstreamSourceService{Now: func() int64 { return 1234 }}).Discover(context.Background(), source.Id)
	require.Error(t, err)

	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, model.UpstreamSourceAuthStatusExpired, session.AuthStatus)
	assert.Equal(t, int64(1234), session.UpdatedTime)
	assert.Contains(t, session.LastAuthError, "401")
	assert.NotContains(t, session.LastAuthError, "super-secret-session")
	assert.Contains(t, session.LastAuthError, "[redacted]")
}

func TestDiscoverAuthenticationChallengePersistsFailedHealth(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	service := UpstreamSourceService{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{err: ErrUpstreamSource2FARequired}, nil
		},
		Now: func() int64 { return 1500 },
	}

	_, err := service.Discover(context.Background(), source.Id)
	require.Error(t, err)
	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, model.UpstreamSourceAuthStatusFailed, session.AuthStatus)
	assert.Equal(t, ErrUpstreamSource2FARequired.Error(), session.LastAuthError)
}

func TestDiscoverNonAuthFailureDoesNotPoisonAuthHealth(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createDiscoveryTestSource(t)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:        source.Id,
		AuthStatus:      model.UpstreamSourceAuthStatusHealthy,
		LastValidatedAt: 1000,
		CreatedTime:     1000,
		UpdatedTime:     1000,
	}))

	service := UpstreamSourceService{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{err: errors.New("dial tcp: connection refused")}, nil
		},
		Now: func() int64 { return 2000 },
	}

	_, err := service.Discover(context.Background(), source.Id)
	require.Error(t, err)

	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, model.UpstreamSourceAuthStatusHealthy, session.AuthStatus)
	assert.Equal(t, int64(1000), session.LastValidatedAt)
	assert.Empty(t, session.LastAuthError)
}

func TestClearUpstreamSourceSessionPreservesCredentialsAndSettings(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:             "source-with-session",
		Type:             model.UpstreamSourceTypeSub2API,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          "https://admin.example.com",
		AdminAPIBasePath: "/api/v1",
		RelayBaseURL:     "https://relay.example.com",
		SyncConfig:       `{"allow_private_ip":true}`,
		AuthConfig:       `{"email":"owner@example.com","password":"long-lived","access_token":"legacy-token","refresh_token":"legacy-refresh","expires_at":9999}`,
	}
	require.NoError(t, model.DB.Create(&source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"current-token","refresh_token":"current-refresh","expires_at":9999}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
		ExpiresAt:     9999,
		CreatedTime:   100,
		UpdatedTime:   100,
	}))

	require.NoError(t, ClearUpstreamSourceSession(source.Id))

	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	assert.Nil(t, session)
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	plaintext, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	assert.Contains(t, plaintext, "owner@example.com")
	assert.Contains(t, plaintext, "long-lived")
	assert.NotContains(t, plaintext, "legacy-token")
	assert.NotContains(t, plaintext, "legacy-refresh")
	assert.NotContains(t, plaintext, "current-token")
	assert.Equal(t, source.Name, reloaded.Name)
	assert.Equal(t, source.BaseURL, reloaded.BaseURL)
	assert.Equal(t, source.RelayBaseURL, reloaded.RelayBaseURL)
	assert.Equal(t, source.SyncConfig, reloaded.SyncConfig)
}

func TestLoadUpstreamSourceRuntimeAuthReadsLegacyMixedConfig(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := model.UpstreamSource{
		Name:         "legacy-source",
		Type:         model.UpstreamSourceTypeSub2API,
		Status:       model.UpstreamSourceStatusEnabled,
		BaseURL:      "https://legacy.example.com",
		RelayBaseURL: "https://legacy.example.com",
		AuthConfig:   `{"email":"legacy@example.com","password":"credential","access_token":"legacy-session","refresh_token":"legacy-refresh","expires_at":54321}`,
	}
	require.NoError(t, model.DB.Create(&source).Error)

	session, err := loadUpstreamSourceRuntimeAuth(&source)
	require.NoError(t, err)
	assert.Nil(t, session, "legacy rows remain a lazy fallback until a successful validation or refresh")
	auth, err := parseSub2APIAuthConfig(&source)
	require.NoError(t, err)
	assert.Equal(t, "legacy@example.com", auth.Email)
	assert.Equal(t, "credential", auth.Password)
	assert.Equal(t, "legacy-session", auth.AccessToken)
	assert.Equal(t, "legacy-refresh", auth.RefreshToken)
	assert.Equal(t, int64(54321), auth.ExpiresAt)
}

func TestSyncAuthFailurePersistsExpiredHealth(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	source := createSyncTestSource(t, nil)
	rate := 1.0
	createSyncTestMapping(t, source.Id, "10", "primary", &rate)
	service := UpstreamSourceService{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamSourceAdapter{createErr: errors.New("upstream request failed with status 401: access_token=sync-secret")}, nil
		},
		Now: func() int64 { return 3000 },
	}

	result, err := service.Sync(context.Background(), source.Id)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, model.UpstreamSyncStatusFailed, result.Status)

	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, model.UpstreamSourceAuthStatusExpired, session.AuthStatus)
	assert.NotContains(t, session.LastAuthError, "sync-secret")
	assert.Contains(t, session.LastAuthError, "[redacted]")
}
