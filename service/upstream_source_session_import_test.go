package service

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// TestApplyImportedSessionNewAPICookieExchangeFailurePropagatesReason is a
// regression guard for deriveNewAPISessionFromImport: a bad/expired pasted
// cookie must surface the real cookie-exchange failure reason (e.g. "session
// did not resolve a user id") instead of being swallowed into the generic
// "provide either an access token + user id, or a session cookie" message,
// which told the admin nothing about why their cookie did not work.
func TestApplyImportedSessionNewAPICookieExchangeFailurePropagatesReason(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/user/self":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":0}}`))
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

	require.Error(t, err)
	assert.Contains(t, err.Error(), "session did not resolve a user id")
	assert.NotContains(t, err.Error(), "provide either an access token")
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
	// "jwt-imported" is not a well-formed JWT and no explicit expires_at was
	// supplied, so expiry is unresolved (0 = never) rather than the old
	// arbitrary now+3600 fallback. See TestSub2APIImportDerivesExpiryFromJWT
	// for the case where the pasted token IS a JWT with an exp claim.
	assert.Equal(t, int64(0), got.ExpiresAt)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	persisted, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	var persistedCfg sub2APIAuthConfig
	require.NoError(t, common.UnmarshalJsonStr(persisted, &persistedCfg))
	assert.Equal(t, "jwt-imported", persistedCfg.AccessToken)
}

// TestSub2APIImportDerivesExpiryFromJWT verifies that a pasted access token
// which happens to be a JWT has its real exp claim used to populate
// expires_at when the admin does not supply one, instead of the previous
// arbitrary "now+3600" fallback that could expire a still-valid session
// early or misreport a longer-lived one.
func TestSub2APIImportDerivesExpiryFromJWT(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	futureExp := time.Now().Add(2 * time.Hour).Unix()
	jwt := buildTestJWT(t, map[string]any{"exp": futureExp})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			assert.Equal(t, "Bearer "+jwt, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":[]}`))
		case "/api/v1/groups/rates":
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
		AccessToken: jwt,
		ExpiresAt:   0,
	})
	require.NoError(t, err)

	got, err := parseSub2APIAuthConfig(source)
	require.NoError(t, err)
	assert.Equal(t, futureExp, got.ExpiresAt, "expires_at should be derived from the JWT exp claim, not now+3600")
}

// buildTestJWT builds an unsigned header.payload.signature JWT string whose
// payload is the base64url (no padding) encoding of claims, matching the
// encoding sub2APIJWTExp expects to decode.
func buildTestJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadBytes, err := common.Marshal(claims)
	require.NoError(t, err)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + ".sig"
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

// TestApplyImportedSessionClearsTurnstileBlockedStatus is a regression guard
// for the confirm-import response: after a successful import, the source's
// LastDiscoveryError/LastSyncError sentinel values must be cleared so
// turnstile_blocked (derived by the controller from those two fields) flips
// to false in the response that confirms the import to the admin, instead of
// staying stuck on the stale Cloudflare Turnstile block.
func TestApplyImportedSessionClearsTurnstileBlockedStatus(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	t.Cleanup(server.Close)

	source := &model.UpstreamSource{
		Type:                model.UpstreamSourceTypeNewAPI,
		Status:              model.UpstreamSourceStatusEnabled,
		BaseURL:             server.URL,
		AdminAPIBasePath:    "/api",
		AuthConfig:          `{"email":"a@b.com","password":"p"}`,
		LastDiscoveryStatus: model.UpstreamDiscoveryStatusFailed,
		LastDiscoveryError:  ErrUpstreamSourceTurnstileRequired.Error(),
		LastSyncStatus:      model.UpstreamSyncStatusFailed,
		LastSyncError:       ErrUpstreamSourceTurnstileRequired.Error(),
	}
	require.NoError(t, model.DB.Create(source).Error)

	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "access-imported",
		UserID:      9,
	})
	require.NoError(t, err)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Empty(t, reloaded.LastDiscoveryError, "a successful import must clear the turnstile sentinel from last_discovery_error")
	assert.Empty(t, reloaded.LastSyncError, "a successful import must clear the turnstile sentinel from last_sync_error")
}

// TestApplyImportedSessionRejectsStaleTokenEvenWhenPasswordLoginWouldSucceed
// reproduces the "rescue" bug: new-api's management request layer
// auto-retries a 401 with a full password re-login whenever the source has
// stored credentials (which import preserves). If the admin's pasted token
// is stale/mistyped but the stored password still works, the naive probe
// (probing with source.AuthConfig, which carries email+password) succeeds
// via the fallback login instead of validating the SPECIFIC pasted session
// -- and the auto-obtained session gets persisted under session_source
// "manual". The probe must use a credentials-stripped copy so the fallback
// login can never fire.
func TestApplyImportedSessionRejectsStaleTokenEvenWhenPasswordLoginWouldSucceed(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/user/self/groups":
			if r.Header.Get("Authorization") == "auto-token" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"invalid access token"}`))
		case r.URL.Path == "/api/user/login":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":99}}`))
		case r.URL.Path == "/api/user/token":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":true,"data":"auto-token"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
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
		AccessToken: "stale-token",
		UserID:      5,
	})
	require.Error(t, err, "a stale pasted token must not be rescued by a fallback password login")

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, originalAuth, reloaded.AuthConfig, "the stored session must be unchanged when the pasted session fails validation")
	assert.NotContains(t, reloaded.AuthConfig, "auto-token")
	assert.NotContains(t, reloaded.AuthConfig, "stale-token")
}
