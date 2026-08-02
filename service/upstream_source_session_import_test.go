package service

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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
	_, err = loadUpstreamSourceRuntimeAuth(&reloaded)
	require.NoError(t, err)
	persistedCfg, err := parseNewAPIAuthConfig(&reloaded)
	require.NoError(t, err)
	assert.Equal(t, "access-imported", persistedCfg.AccessToken, "the imported session must be persisted so discover/sync reuse it instead of logging in again")
	assert.Equal(t, 9, persistedCfg.UserID)
}

func TestApplyImportedSessionAdvancesAuthRevisionAndInvalidatesStaleMonitor(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/user/self/groups", r.URL.Path)
		assert.Equal(t, "imported-session", r.Header.Get("Authorization"))
		assert.Equal(t, "19", r.Header.Get("New-Api-User"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	t.Cleanup(server.Close)

	const startingRevision int64 = 7
	source := &model.UpstreamSource{
		Name:                   "import-during-monitor",
		Type:                   model.UpstreamSourceTypeNewAPI,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                server.URL,
		AdminAPIBasePath:       "/api",
		AuthConfig:             `{"email":"owner@example.com","password":"credential"}`,
		AuthRevision:           startingRevision,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          1000,
	}
	require.NoError(t, model.DB.Create(source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"monitor-session","user_id":3,"session_source":"login"}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))

	monitorStarted := make(chan struct{}, 1)
	releaseMonitor := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseMonitor) }) })
	runner := UpstreamSourceMonitorRunner{
		AdapterFactory: func(string) (UpstreamSourceAdapter, error) {
			return fakeUpstreamBalanceAdapter{collect: func(context.Context, *model.UpstreamSource) (UpstreamBalanceSnapshot, error) {
				monitorStarted <- struct{}{}
				<-releaseMonitor
				return UpstreamBalanceSnapshot{Available: 1, Currency: "USD"}, nil
			}}, nil
		},
		Now: func() int64 { return 2000 },
	}
	monitorDone := make(chan []UpstreamSourceMonitorResult, 1)
	go func() { monitorDone <- runner.RunDue(context.Background(), 1000) }()
	<-monitorStarted

	var claimed model.UpstreamSource
	require.NoError(t, model.DB.First(&claimed, source.Id).Error)
	require.NotEmpty(t, claimed.CurrentMonitorToken)
	claimToken := claimed.CurrentMonitorToken

	importStartedAt := common.GetTimestamp()
	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "imported-session",
		UserID:      19,
	})
	importFinishedAt := common.GetTimestamp()
	require.NoError(t, err)

	var imported model.UpstreamSource
	require.NoError(t, model.DB.First(&imported, source.Id).Error)
	assert.Equal(t, startingRevision+1, imported.AuthRevision)
	assert.Equal(t, claimToken, imported.CurrentMonitorToken, "manual import must leave an active monitor claim owned until normal release")
	assert.GreaterOrEqual(t, imported.NextMonitorAt, importStartedAt)
	assert.LessOrEqual(t, imported.NextMonitorAt, importFinishedAt)
	_, err = loadUpstreamSourceRuntimeAuth(&imported)
	require.NoError(t, err)
	importedAuth, err := parseNewAPIAuthConfig(&imported)
	require.NoError(t, err)
	assert.Equal(t, "imported-session", importedAuth.AccessToken)
	assert.Equal(t, 19, importedAuth.UserID)

	releaseOnce.Do(func() { close(releaseMonitor) })
	results := <-monitorDone
	require.Len(t, results, 1)

	var afterMonitor model.UpstreamSource
	require.NoError(t, model.DB.First(&afterMonitor, source.Id).Error)
	assert.Equal(t, startingRevision+1, afterMonitor.AuthRevision)
	assert.Empty(t, afterMonitor.CurrentMonitorToken)
	assert.Equal(t, imported.NextMonitorAt, afterMonitor.NextMonitorAt, "the stale monitor release must preserve the import requeue")
	_, err = loadUpstreamSourceRuntimeAuth(&afterMonitor)
	require.NoError(t, err)
	afterMonitorAuth, err := parseNewAPIAuthConfig(&afterMonitor)
	require.NoError(t, err)
	assert.Equal(t, "imported-session", afterMonitorAuth.AccessToken, "the stale monitor must not overwrite the imported session")
	assert.Equal(t, 19, afterMonitorAuth.UserID)
}

func TestApplyImportedSessionRequeuesParkedEnabledMonitor(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	t.Cleanup(server.Close)

	const startingRevision int64 = 11
	source := &model.UpstreamSource{
		Name:                   "parked-import",
		Type:                   model.UpstreamSourceTypeNewAPI,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                server.URL,
		AdminAPIBasePath:       "/api",
		AuthConfig:             `{"email":"owner@example.com","password":"credential"}`,
		AuthRevision:           startingRevision,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          model.UpstreamSourceMonitorParkedSchedule,
		MonitorParkedReason:    model.UpstreamSourceMonitorParkedReasonCredentialDecryption,
	}
	require.NoError(t, model.DB.Create(source).Error)

	startedAt := common.GetTimestamp()
	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "imported-session",
		UserID:      23,
	})
	finishedAt := common.GetTimestamp()
	require.NoError(t, err)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, startingRevision+1, reloaded.AuthRevision)
	assert.GreaterOrEqual(t, reloaded.NextMonitorAt, startedAt)
	assert.LessOrEqual(t, reloaded.NextMonitorAt, finishedAt)
	assert.Empty(t, reloaded.CurrentMonitorToken)
	assert.Empty(t, reloaded.MonitorParkedReason)
}

func TestApplyImportedSessionRejectsConcurrentCredentialRepair(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	validationStarted := make(chan struct{}, 1)
	releaseValidation := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		validationStarted <- struct{}{}
		<-releaseValidation
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseValidation) }) })

	const startingRevision int64 = 17
	source := &model.UpstreamSource{
		Name:             "import-repair-race",
		Type:             model.UpstreamSourceTypeNewAPI,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api",
		AuthConfig:       `{"email":"old@example.com","password":"old-credential"}`,
		AuthRevision:     startingRevision,
	}
	require.NoError(t, model.DB.Create(source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"old-session","user_id":5,"session_source":"login"}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))

	importDone := make(chan error, 1)
	go func() {
		importDone <- ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
			AccessToken: "stale-imported-session",
			UserID:      29,
		})
	}()
	<-validationStarted

	repairedConfig, err := WriteUpstreamSourceAuthConfig(`{"email":"repaired@example.com","password":"replacement"}`)
	require.NoError(t, err)
	repaired, err := UpdateUpstreamSourceCredentials(source.Id, repairedConfig, 4000)
	require.NoError(t, err)
	assert.Equal(t, startingRevision+1, repaired.AuthRevision)

	releaseOnce.Do(func() { close(releaseValidation) })
	err = <-importDone
	require.EqualError(t, err, "upstream source authentication state changed during session import")

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, startingRevision+1, reloaded.AuthRevision)
	plaintext, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	var persisted newAPIAuthConfig
	require.NoError(t, common.UnmarshalJsonStr(plaintext, &persisted))
	assert.Equal(t, "repaired@example.com", persisted.Email)
	assert.Equal(t, "replacement", persisted.Password)
	assert.Empty(t, persisted.AccessToken)
	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	assert.Nil(t, session)
}

func TestApplyImportedSessionValidationFailureDoesNotPoisonConcurrentRepair(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	validationStarted := make(chan struct{}, 1)
	releaseValidation := make(chan struct{})
	var releaseOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		validationStarted <- struct{}{}
		<-releaseValidation
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"message":"invalid access_token=stale-validation-secret"}`))
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseValidation) }) })

	const startingRevision int64 = 31
	source := &model.UpstreamSource{
		Name:             "failed-import-repair-race",
		Type:             model.UpstreamSourceTypeNewAPI,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api",
		AuthConfig:       `{"email":"old@example.com","password":"old-credential"}`,
		AuthRevision:     startingRevision,
	}
	require.NoError(t, model.DB.Create(source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"old-session","user_id":7,"session_source":"login"}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))

	importDone := make(chan error, 1)
	go func() {
		importDone <- ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
			AccessToken: "rejected-imported-session",
			UserID:      37,
		})
	}()
	<-validationStarted

	repairedConfig, err := WriteUpstreamSourceAuthConfig(`{"email":"repaired@example.com","password":"replacement"}`)
	require.NoError(t, err)
	_, err = UpdateUpstreamSourceCredentials(source.Id, repairedConfig, 5000)
	require.NoError(t, err)

	releaseOnce.Do(func() { close(releaseValidation) })
	err = <-importDone
	require.EqualError(t, err, "upstream source authentication state changed during session import")

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, startingRevision+1, reloaded.AuthRevision)
	plaintext, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	var persisted newAPIAuthConfig
	require.NoError(t, common.UnmarshalJsonStr(plaintext, &persisted))
	assert.Equal(t, "repaired@example.com", persisted.Email)
	assert.Equal(t, "replacement", persisted.Password)
	assert.Empty(t, persisted.AccessToken)
	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	assert.Nil(t, session, "the stale validation failure must not recreate auth health cleared by credential repair")
}

func TestApplyImportedSessionReplacesExistingDedicatedSession(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "replacement-token", r.Header.Get("Authorization"))
		assert.Equal(t, "17", r.Header.Get("New-Api-User"))
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
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"old-token","user_id":9,"session_source":"manual"}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))

	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "replacement-token",
		UserID:      17,
	})
	require.NoError(t, err)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	_, err = loadUpstreamSourceRuntimeAuth(&reloaded)
	require.NoError(t, err)
	auth, err := parseNewAPIAuthConfig(&reloaded)
	require.NoError(t, err)
	assert.Equal(t, "replacement-token", auth.AccessToken)
	assert.Equal(t, 17, auth.UserID)
}

func TestApplyImportedSessionReplacesCorruptDedicatedSessionAndClearsPark(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)
	oldStable := common.CryptoSecretStable
	oldSecret := common.CryptoSecret
	common.CryptoSecretStable = true
	common.CryptoSecret = "upstream-source-import-corrupt-session-test"
	t.Cleanup(func() {
		common.CryptoSecretStable = oldStable
		common.CryptoSecret = oldSecret
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "replacement-token", r.Header.Get("Authorization"))
		assert.Equal(t, "43", r.Header.Get("New-Api-User"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	t.Cleanup(server.Close)

	const startingRevision int64 = 41
	source := &model.UpstreamSource{
		Name:                   "corrupt-dedicated-session",
		Type:                   model.UpstreamSourceTypeNewAPI,
		Status:                 model.UpstreamSourceStatusEnabled,
		BaseURL:                server.URL,
		AdminAPIBasePath:       "/api",
		AuthConfig:             `{"email":"owner@example.com","password":"credential"}`,
		AuthRevision:           startingRevision,
		MonitorEnabled:         true,
		MonitorIntervalMinutes: 10,
		NextMonitorAt:          model.UpstreamSourceMonitorParkedSchedule,
		MonitorParkedReason:    model.UpstreamSourceMonitorParkedReasonCredentialDecryption,
		CurrentMonitorToken:    "active-monitor",
		MonitorStartedAt:       1000,
	}
	require.NoError(t, model.DB.Create(source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: corruptedUpstreamSourceSecretEnvelope,
		AuthStatus:    model.UpstreamSourceAuthStatusFailed,
	}))

	startedAt := common.GetTimestamp()
	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "replacement-token",
		UserID:      43,
	})
	finishedAt := common.GetTimestamp()
	require.NoError(t, err)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, startingRevision+1, reloaded.AuthRevision)
	assert.GreaterOrEqual(t, reloaded.NextMonitorAt, startedAt)
	assert.LessOrEqual(t, reloaded.NextMonitorAt, finishedAt)
	assert.Equal(t, "active-monitor", reloaded.CurrentMonitorToken)
	assert.Empty(t, reloaded.MonitorParkedReason)
	_, err = loadUpstreamSourceRuntimeAuth(&reloaded)
	require.NoError(t, err)
	auth, err := parseNewAPIAuthConfig(&reloaded)
	require.NoError(t, err)
	assert.Equal(t, "owner@example.com", auth.Email)
	assert.Equal(t, "credential", auth.Password)
	assert.Equal(t, "replacement-token", auth.AccessToken)
	assert.Equal(t, 43, auth.UserID)
	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.NotEqual(t, corruptedUpstreamSourceSecretEnvelope, session.SessionConfig)
	_, err = ReadUpstreamSourceAuthConfig(session.SessionConfig)
	require.NoError(t, err)
}

func TestApplyImportedSessionRejectsCorruptCredentialConfigWithStableSentinel(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)
	oldStable := common.CryptoSecretStable
	oldSecret := common.CryptoSecret
	common.CryptoSecretStable = true
	common.CryptoSecret = "upstream-source-import-corrupt-credential-test"
	t.Cleanup(func() {
		common.CryptoSecretStable = oldStable
		common.CryptoSecret = oldSecret
	})

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)

	const startingRevision int64 = 47
	source := &model.UpstreamSource{
		Name:             "corrupt-credentials",
		Type:             model.UpstreamSourceTypeNewAPI,
		Status:           model.UpstreamSourceStatusEnabled,
		BaseURL:          server.URL,
		AdminAPIBasePath: "/api",
		AuthConfig:       corruptedUpstreamSourceSecretEnvelope,
		AuthRevision:     startingRevision,
	}
	require.NoError(t, model.DB.Create(source).Error)
	require.NoError(t, model.UpsertUpstreamSourceSessionTx(model.DB, &model.UpstreamSourceSession{
		SourceID:      source.Id,
		SessionConfig: `{"access_token":"old-session","user_id":5}`,
		AuthStatus:    model.UpstreamSourceAuthStatusHealthy,
	}))

	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "replacement-token",
		UserID:      53,
	})

	require.EqualError(t, err, "upstream source credential decryption failed")
	assert.Zero(t, requests.Load())
	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	assert.Equal(t, startingRevision, reloaded.AuthRevision)
	assert.Equal(t, corruptedUpstreamSourceSecretEnvelope, reloaded.AuthConfig)
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
	_, err = loadUpstreamSourceRuntimeAuth(&reloaded)
	require.NoError(t, err)
	persistedCfg, err := parseSub2APIAuthConfig(&reloaded)
	require.NoError(t, err)
	assert.Equal(t, "jwt-imported", persistedCfg.AccessToken)
}

func TestApplyImportedSessionPersistsHealthyAuthStateSeparately(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	expiresAt := time.Now().Add(2 * time.Hour).Unix()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/groups/available":
			assert.Equal(t, "Bearer imported-session", r.Header.Get("Authorization"))
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
		AuthConfig:       `{"email":"a@b.com","password":"long-lived-secret"}`,
	}
	require.NoError(t, model.DB.Create(source).Error)
	startedAt := time.Now().Unix()

	err := ApplyUpstreamSourceImportedSession(context.Background(), source, dto.UpstreamSourceSessionImportRequest{
		AccessToken: "imported-session",
		ExpiresAt:   expiresAt,
	})
	require.NoError(t, err)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	sourceAuth, err := ReadUpstreamSourceAuthConfig(reloaded.AuthConfig)
	require.NoError(t, err)
	assert.Contains(t, sourceAuth, "a@b.com")
	assert.Contains(t, sourceAuth, "long-lived-secret")
	assert.NotContains(t, sourceAuth, "imported-session")
	assert.NotContains(t, sourceAuth, "access_token")
	assert.NotContains(t, sourceAuth, "refresh_token")

	session, err := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, err)
	require.NotNil(t, session)
	assert.Equal(t, model.UpstreamSourceAuthStatusHealthy, session.AuthStatus)
	assert.GreaterOrEqual(t, session.LastValidatedAt, startedAt)
	assert.GreaterOrEqual(t, session.LastRefreshedAt, startedAt)
	assert.Equal(t, expiresAt, session.ExpiresAt)
	assert.Empty(t, session.LastAuthError)
	sessionAuth, err := ReadUpstreamSourceAuthConfig(session.SessionConfig)
	require.NoError(t, err)
	assert.Contains(t, sessionAuth, "imported-session")
	assert.NotContains(t, sessionAuth, "long-lived-secret")
	assert.NotContains(t, sessionAuth, "email")
	assert.NotContains(t, sessionAuth, "password")
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

// TestApplyImportedSessionSub2APIRefreshesStaleAccessToken reproduces a
// "double rescue" bug: if the pasted access token is already expired but a
// refresh token is present, the live probe (DiscoverGroups -> ensureAccessToken)
// renews the session in place via /auth/refresh, mutating source.AuthConfig
// to the FRESH session. Persisting the pre-probe finalJSON afterwards would
// throw that refreshed session away and store the stale pasted access token
// instead -- dead on arrival if the gateway issues one-time refresh tokens.
// The persisted config must be the post-probe (refreshed) session, with the
// admin's stored email/password merged back in.
func TestApplyImportedSessionSub2APIRefreshesStaleAccessToken(t *testing.T) {
	setupUpstreamSourceServiceTestDB(t)
	withSub2APIFetchSetting(t, true)

	pastExpiry := time.Now().Add(-1 * time.Hour).Unix()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/refresh":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":{"access_token":"refreshed-token","refresh_token":"new-refresh-token","expires_in":3600}}`))
		case "/api/v1/groups/available":
			assert.Equal(t, "Bearer refreshed-token", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"code":0,"message":"","data":[]}`))
		case "/api/v1/groups/rates":
			assert.Equal(t, "Bearer refreshed-token", r.Header.Get("Authorization"))
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
		AccessToken:  "stale-pasted-token",
		RefreshToken: "valid-refresh-token",
		ExpiresAt:    pastExpiry,
	})
	require.NoError(t, err)

	got, err := parseSub2APIAuthConfig(source)
	require.NoError(t, err)
	assert.Equal(t, "refreshed-token", got.AccessToken, "the refreshed session must be persisted, not the stale pasted access token")
	assert.Equal(t, "a@b.com", got.Email, "stored credentials must survive a probe that refreshed the session")
	assert.Equal(t, "p", got.Password)

	var reloaded model.UpstreamSource
	require.NoError(t, model.DB.First(&reloaded, source.Id).Error)
	_, err = loadUpstreamSourceRuntimeAuth(&reloaded)
	require.NoError(t, err)
	persistedCfg, err := parseSub2APIAuthConfig(&reloaded)
	require.NoError(t, err)
	assert.Equal(t, "refreshed-token", persistedCfg.AccessToken)
	assert.NotEqual(t, "stale-pasted-token", persistedCfg.AccessToken)
	assert.Equal(t, "a@b.com", persistedCfg.Email)
	assert.Equal(t, "p", persistedCfg.Password)
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
	session, sessionErr := model.GetUpstreamSourceSession(source.Id)
	require.NoError(t, sessionErr)
	require.NotNil(t, session)
	assert.Equal(t, model.UpstreamSourceAuthStatusExpired, session.AuthStatus)
	assert.NotContains(t, session.LastAuthError, "bad-token")
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
