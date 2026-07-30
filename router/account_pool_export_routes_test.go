package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var accountPoolExportRouteTestCounter atomic.Uint32

type accountPoolExportRouteFixture struct {
	engine          *gin.Engine
	db              *gorm.DB
	poolID          int
	otherPoolID     int
	credentialValue string
	otherCredential string
	root            model.User
	admin           model.User
}

type accountPoolExportRouteResponse struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Accounts []struct {
			Credentials map[string]any `json:"credentials"`
		} `json:"accounts"`
	} `json:"data"`
}

type accountPoolVerificationRouteResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    struct {
		ProofToken string `json:"proof_token"`
	} `json:"data"`
}

func setupAccountPoolExportRouteTest(t *testing.T, criticalRateLimit bool) accountPoolExportRouteFixture {
	t.Helper()

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldCriticalRateLimitEnable := common.CriticalRateLimitEnable
	oldCriticalRateLimitNum := common.CriticalRateLimitNum
	oldCriticalRateLimitDuration := common.CriticalRateLimitDuration
	oldGlobalAPIRateLimitEnable := common.GlobalApiRateLimitEnable
	oldTranslateMessage := common.TranslateMessage
	oldCryptoSecret := common.CryptoSecret
	oldCryptoSecretStable := common.CryptoSecretStable
	oldSessionSecret := common.SessionSecret
	oldMainDBType := common.MainDatabaseType()

	testID := accountPoolExportRouteTestCounter.Add(1)
	dsn := filepath.Join(t.TempDir(), fmt.Sprintf("account-pool-export-routes-%d.db", testID)) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)

	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.CriticalRateLimitEnable = criticalRateLimit
	common.CriticalRateLimitNum = 1
	common.CriticalRateLimitDuration = 60
	common.GlobalApiRateLimitEnable = false
	common.TranslateMessage = func(_ *gin.Context, key string, _ ...map[string]any) string { return key }
	common.CryptoSecret = "account-pool-export-encryption-fixture"
	common.CryptoSecretStable = true
	common.SessionSecret = "account-pool-export-session-signing-fixture"
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)

	t.Cleanup(func() {
		middleware.DrainAdminAuditJobs()
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.CriticalRateLimitEnable = oldCriticalRateLimitEnable
		common.CriticalRateLimitNum = oldCriticalRateLimitNum
		common.CriticalRateLimitDuration = oldCriticalRateLimitDuration
		common.GlobalApiRateLimitEnable = oldGlobalAPIRateLimitEnable
		common.TranslateMessage = oldTranslateMessage
		common.CryptoSecret = oldCryptoSecret
		common.CryptoSecretStable = oldCryptoSecretStable
		common.SessionSecret = oldSessionSecret
		common.SetMainDatabaseType(oldMainDBType)
		require.NoError(t, sqlDB.Close())
	})

	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserSession{},
		&model.AuthFlow{},
		&model.TwoFA{},
		&model.TwoFABackupCode{},
		&model.Log{},
		&model.AccountPool{},
		&model.AccountPoolAccount{},
		&model.AccountPoolProxy{},
	))
	require.NoError(t, model.EnsureAccountPoolAccountColumnsSQLite())

	rootPAT := "11111111111111111111111111111111"
	adminPAT := "22222222222222222222222222222222"
	root := model.User{
		Id: 1, Username: "root-export-fixture", Password: "password-placeholder",
		Role: common.RoleRootUser, Status: common.UserStatusEnabled, Group: "default",
		AuthVersion: 1, AffCode: "root-export-fixture", AccessToken: &rootPAT,
	}
	admin := model.User{
		Id: 2, Username: "admin-export-fixture", Password: "password-placeholder",
		Role: common.RoleAdminUser, Status: common.UserStatusEnabled, Group: "default",
		AuthVersion: 1, AffCode: "admin-export-fixture", AccessToken: &adminPAT,
	}
	require.NoError(t, db.Create(&[]model.User{root, admin}).Error)

	svc := service.AccountPoolService{}
	pool, err := svc.CreatePool(service.AccountPoolCreateParams{
		Name:     "export-route-fixture",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.NoError(t, err)
	credentialValue := "account-pool-export-credential-fixture"
	_, err = svc.CreateAccount(service.AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "export-account-fixture",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: credentialValue,
		},
	})
	require.NoError(t, err)
	otherPool, err := svc.CreatePool(service.AccountPoolCreateParams{
		Name:     "other-export-route-fixture",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.NoError(t, err)
	otherCredential := "other-account-pool-export-credential-fixture"
	_, err = svc.CreateAccount(service.AccountPoolAccountCreateParams{
		PoolID: otherPool.Id,
		Name:   "other-export-account-fixture",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: otherCredential,
		},
	})
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	return accountPoolExportRouteFixture{
		engine:          engine,
		db:              db,
		poolID:          pool.Id,
		otherPoolID:     otherPool.Id,
		credentialValue: credentialValue,
		otherCredential: otherCredential,
		root:            root,
		admin:           admin,
	}
}

func accountPoolExportResourceScope(poolID int) string {
	return fmt.Sprintf("%s:pool:%d", service.SecurityProofScopeAccountPoolCredentialsExport, poolID)
}

func (fixture accountPoolExportRouteFixture) sessionAuth(t *testing.T, user model.User) string {
	t.Helper()
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "account-pool-export-route-test")
	require.NoError(t, err)
	return bundle.AccessToken
}

func (fixture accountPoolExportRouteFixture) sessionAuthWithProof(t *testing.T, user model.User, method string, scopes ...string) (string, string) {
	t.Helper()
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "account-pool-export-route-test")
	require.NoError(t, err)
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	proof, _, err := service.IssueSecurityProof(identity, method, scopes)
	require.NoError(t, err)
	return bundle.AccessToken, proof
}

func (fixture accountPoolExportRouteFixture) request(t *testing.T, user model.User, accessToken string, proof string, includeSecrets bool, remoteAddr string) (*httptest.ResponseRecorder, accountPoolExportRouteResponse) {
	t.Helper()
	recorder, response, err := fixture.doRequest(fixture.poolID, user, accessToken, proof, includeSecrets, remoteAddr)
	require.NoError(t, err)
	return recorder, response
}

func (fixture accountPoolExportRouteFixture) requestPool(t *testing.T, poolID int, user model.User, accessToken string, proof string, includeSecrets bool, remoteAddr string) (*httptest.ResponseRecorder, accountPoolExportRouteResponse) {
	t.Helper()
	recorder, response, err := fixture.doRequest(poolID, user, accessToken, proof, includeSecrets, remoteAddr)
	require.NoError(t, err)
	return recorder, response
}

func (fixture accountPoolExportRouteFixture) doRequest(poolID int, user model.User, accessToken string, proof string, includeSecrets bool, remoteAddr string) (*httptest.ResponseRecorder, accountPoolExportRouteResponse, error) {
	target := fmt.Sprintf("/api/account_pools/%d/accounts/export", poolID)
	if includeSecrets {
		target += "?include_secrets=true"
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.Header.Set("New-Api-User", strconv.Itoa(user.Id))
	request.Header.Set("Authorization", "Bearer "+accessToken)
	if proof != "" {
		request.Header.Set("X-Security-Proof", proof)
	}
	if remoteAddr != "" {
		request.RemoteAddr = remoteAddr
	}
	recorder := httptest.NewRecorder()
	fixture.engine.ServeHTTP(recorder, request)

	var response accountPoolExportRouteResponse
	if recorder.Body.Len() > 0 {
		if err := common.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			return recorder, response, err
		}
	}
	return recorder, response, nil
}

func (fixture accountPoolExportRouteFixture) verify2FA(t *testing.T, accessToken, code string) (*httptest.ResponseRecorder, accountPoolVerificationRouteResponse) {
	t.Helper()
	body, err := common.Marshal(map[string]string{
		"method": "2fa",
		"code":   code,
		"scope":  service.SecurityProofScopeAccountPoolCredentialsExport,
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/api/verify", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("New-Api-User", strconv.Itoa(fixture.root.Id))
	request.Header.Set("Authorization", "Bearer "+accessToken)
	recorder := httptest.NewRecorder()
	fixture.engine.ServeHTTP(recorder, request)

	var response accountPoolVerificationRouteResponse
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder, response
}

func assertAccountPoolExportNoStore(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	assert.Equal(t, "no-store, no-cache, must-revalidate, private, max-age=0", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, "no-cache", recorder.Header().Get("Pragma"))
	assert.Equal(t, "0", recorder.Header().Get("Expires"))
}

func assertAccountPoolCredentialsAbsent(t *testing.T, recorder *httptest.ResponseRecorder, credentials ...string) {
	t.Helper()
	for _, credential := range credentials {
		assert.NotContains(t, recorder.Body.String(), credential)
	}
}

func TestAccountPoolSecretExportRejectsOrdinaryAdminPATAndSession(t *testing.T) {
	tests := []struct {
		name string
		auth func(t *testing.T, fixture accountPoolExportRouteFixture) (string, string)
	}{
		{
			name: "personal access token",
			auth: func(_ *testing.T, fixture accountPoolExportRouteFixture) (string, string) {
				return fixture.admin.GetAccessToken(), ""
			},
		},
		{
			name: "dashboard session with export proof",
			auth: func(t *testing.T, fixture accountPoolExportRouteFixture) (string, string) {
				return fixture.sessionAuthWithProof(
					t,
					fixture.admin,
					"passkey",
					service.SecurityProofScopeAccountPoolCredentialsExport,
					accountPoolExportResourceScope(fixture.poolID),
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := setupAccountPoolExportRouteTest(t, false)
			accessToken, proof := test.auth(t, fixture)
			recorder, response := fixture.request(t, fixture.admin, accessToken, proof, true, "")

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.False(t, response.Success)
			assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
			assertAccountPoolExportNoStore(t, recorder)
		})
	}
}

func TestAccountPoolSecretExportRejectsRootWithoutActionProof(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)
	accessToken := fixture.sessionAuth(t, fixture.root)

	recorder, response := fixture.request(t, fixture.root, accessToken, "", true, "")

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, "SECURITY_PROOF_REQUIRED", response.Code)
	assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
	assertAccountPoolExportNoStore(t, recorder)
}

func TestAccountPoolSecretExportRejectsRootPAT(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)
	_, proof := fixture.sessionAuthWithProof(
		t,
		fixture.root,
		"passkey",
		service.SecurityProofScopeAccountPoolCredentialsExport,
		accountPoolExportResourceScope(fixture.poolID),
	)

	recorder, response := fixture.request(t, fixture.root, fixture.root.GetAccessToken(), proof, true, "")

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, "SECURITY_PROOF_INVALID", response.Code)
	assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
	assertAccountPoolExportNoStore(t, recorder)
}

func TestAccountPoolSecretExportRequiresActionSpecificProofAndDisablesCaching(t *testing.T) {
	t.Run("channel proof is rejected", func(t *testing.T) {
		fixture := setupAccountPoolExportRouteTest(t, false)
		accessToken, proof := fixture.sessionAuthWithProof(t, fixture.root, "passkey", "channel.key.read")

		recorder, response := fixture.request(t, fixture.root, accessToken, proof, true, "")

		assert.Equal(t, http.StatusForbidden, recorder.Code)
		assert.Equal(t, "SECURITY_PROOF_SCOPE_MISMATCH", response.Code)
		assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
		assertAccountPoolExportNoStore(t, recorder)
	})

	t.Run("resource-bound passkey proof succeeds", func(t *testing.T) {
		fixture := setupAccountPoolExportRouteTest(t, false)
		accessToken, proof := fixture.sessionAuthWithProof(
			t,
			fixture.root,
			"passkey",
			service.SecurityProofScopeAccountPoolCredentialsExport,
			accountPoolExportResourceScope(fixture.poolID),
		)

		recorder, response := fixture.request(t, fixture.root, accessToken, proof, true, "")

		require.Equal(t, http.StatusOK, recorder.Code)
		require.True(t, response.Success)
		require.Len(t, response.Data.Accounts, 1)
		assert.Equal(t, fixture.credentialValue, response.Data.Accounts[0].Credentials["api_key"])
		assertAccountPoolExportNoStore(t, recorder)
	})
}

func TestAccountPoolSecretExportRejectsTOTPAndRecoveryVerification(t *testing.T) {
	const (
		totpSecret   = "JBSWY3DPEHPK3PXP"
		recoveryCode = "ABCD-1234"
	)
	tests := []struct {
		name string
		code func(t *testing.T) string
	}{
		{
			name: "totp",
			code: func(t *testing.T) string {
				code, err := totp.GenerateCode(totpSecret, time.Now())
				require.NoError(t, err)
				return code
			},
		},
		{
			name: "recovery code",
			code: func(_ *testing.T) string { return recoveryCode },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := setupAccountPoolExportRouteTest(t, false)
			require.NoError(t, fixture.db.Create(&model.TwoFA{
				UserId: fixture.root.Id, Secret: totpSecret, IsEnabled: true,
			}).Error)
			recoveryHash, err := common.HashBackupCode(recoveryCode)
			require.NoError(t, err)
			require.NoError(t, fixture.db.Create(&model.TwoFABackupCode{
				UserId: fixture.root.Id, CodeHash: recoveryHash,
			}).Error)
			accessToken := fixture.sessionAuth(t, fixture.root)

			verificationRecorder, verification := fixture.verify2FA(t, accessToken, test.code(t))

			require.Equal(t, http.StatusOK, verificationRecorder.Code)
			assert.False(t, verification.Success)
			assert.False(t, verification.Data.ProofToken != "", "verification response must not contain a proof")
			assertAccountPoolCredentialsAbsent(t, verificationRecorder, fixture.credentialValue, fixture.otherCredential)
			assertAccountPoolExportNoStore(t, verificationRecorder)
			if test.name == "recovery code" {
				var unused int64
				require.NoError(t, fixture.db.Model(&model.TwoFABackupCode{}).
					Where("user_id = ? AND is_used = ?", fixture.root.Id, false).
					Count(&unused).Error)
				assert.Equal(t, int64(1), unused, "rejected export verification must not consume a recovery code")
			}

			exportRecorder, exportResponse := fixture.request(t, fixture.root, accessToken, verification.Data.ProofToken, true, "")
			assert.Equal(t, http.StatusForbidden, exportRecorder.Code)
			assert.False(t, exportResponse.Success)
			assertAccountPoolCredentialsAbsent(t, exportRecorder, fixture.credentialValue, fixture.otherCredential)
			assertAccountPoolExportNoStore(t, exportRecorder)
		})
	}
}

func TestAccountPoolSecretExportProofCannotBeReplayedAcrossClientIPs(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)
	accessToken, proof := fixture.sessionAuthWithProof(
		t,
		fixture.root,
		"passkey",
		service.SecurityProofScopeAccountPoolCredentialsExport,
		accountPoolExportResourceScope(fixture.poolID),
	)

	first, firstResponse := fixture.request(t, fixture.root, accessToken, proof, true, "192.0.2.10:1234")
	require.Equal(t, http.StatusOK, first.Code)
	require.True(t, firstResponse.Success)
	require.Contains(t, first.Body.String(), fixture.credentialValue)
	assertAccountPoolExportNoStore(t, first)

	second, secondResponse := fixture.request(t, fixture.root, accessToken, proof, true, "198.51.100.20:4321")
	assert.Equal(t, http.StatusForbidden, second.Code)
	assert.False(t, secondResponse.Success)
	assertAccountPoolCredentialsAbsent(t, second, fixture.credentialValue, fixture.otherCredential)
	assertAccountPoolExportNoStore(t, second)
}

func TestAccountPoolSecretExportConcurrentProofUseAllowsOneSuccess(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)
	accessToken, proof := fixture.sessionAuthWithProof(
		t,
		fixture.root,
		"passkey",
		service.SecurityProofScopeAccountPoolCredentialsExport,
		accountPoolExportResourceScope(fixture.poolID),
	)

	type result struct {
		recorder *httptest.ResponseRecorder
		response accountPoolExportRouteResponse
		err      error
	}
	const requests = 8
	start := make(chan struct{})
	results := make(chan result, requests)
	var waitGroup sync.WaitGroup
	for i := 0; i < requests; i++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			<-start
			recorder, response, err := fixture.doRequest(
				fixture.poolID,
				fixture.root,
				accessToken,
				proof,
				true,
				fmt.Sprintf("203.0.113.%d:1234", index+1),
			)
			results <- result{recorder: recorder, response: response, err: err}
		}(i)
	}
	close(start)
	waitGroup.Wait()
	close(results)

	successes := 0
	for requestResult := range results {
		require.NoError(t, requestResult.err)
		assertAccountPoolExportNoStore(t, requestResult.recorder)
		if requestResult.recorder.Code == http.StatusOK && requestResult.response.Success {
			successes++
			assert.Contains(t, requestResult.recorder.Body.String(), fixture.credentialValue)
			continue
		}
		assert.Equal(t, http.StatusForbidden, requestResult.recorder.Code)
		assertAccountPoolCredentialsAbsent(t, requestResult.recorder, fixture.credentialValue, fixture.otherCredential)
	}
	assert.Equal(t, 1, successes)
}

func TestAccountPoolSecretExportProofIsBoundToExactPool(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)
	accessToken, proof := fixture.sessionAuthWithProof(
		t,
		fixture.root,
		"passkey",
		service.SecurityProofScopeAccountPoolCredentialsExport,
		accountPoolExportResourceScope(fixture.poolID),
	)

	recorder, response := fixture.requestPool(t, fixture.otherPoolID, fixture.root, accessToken, proof, true, "")

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.False(t, response.Success)
	assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
	assertAccountPoolExportNoStore(t, recorder)
}

func TestAccountPoolSecretExportFailsClosedWhenReplayStoreFails(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)
	accessToken, proof := fixture.sessionAuthWithProof(
		t,
		fixture.root,
		"passkey",
		service.SecurityProofScopeAccountPoolCredentialsExport,
		accountPoolExportResourceScope(fixture.poolID),
	)
	require.NoError(t, fixture.db.Migrator().DropTable(&model.AuthFlow{}))

	recorder, response := fixture.request(t, fixture.root, accessToken, proof, true, "")

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.False(t, response.Success)
	assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
	assertAccountPoolExportNoStore(t, recorder)
}

func TestAccountPoolSecretExportRejectsWrongPrincipalSessionAndVersion(t *testing.T) {
	t.Run("non-root", func(t *testing.T) {
		fixture := setupAccountPoolExportRouteTest(t, false)
		accessToken, proof := fixture.sessionAuthWithProof(
			t,
			fixture.admin,
			"passkey",
			service.SecurityProofScopeAccountPoolCredentialsExport,
			accountPoolExportResourceScope(fixture.poolID),
		)

		recorder, response := fixture.request(t, fixture.admin, accessToken, proof, true, "")

		assert.Equal(t, http.StatusForbidden, recorder.Code)
		assert.False(t, response.Success)
		assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
		assertAccountPoolExportNoStore(t, recorder)
	})

	t.Run("wrong session", func(t *testing.T) {
		fixture := setupAccountPoolExportRouteTest(t, false)
		firstAccess, proof := fixture.sessionAuthWithProof(
			t,
			fixture.root,
			"passkey",
			service.SecurityProofScopeAccountPoolCredentialsExport,
			accountPoolExportResourceScope(fixture.poolID),
		)
		secondAccess := fixture.sessionAuth(t, fixture.root)

		recorder, response := fixture.request(t, fixture.root, secondAccess, proof, true, "")

		assert.Equal(t, http.StatusForbidden, recorder.Code)
		assert.False(t, response.Success)
		assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
		assertAccountPoolExportNoStore(t, recorder)

		validRecorder, validResponse := fixture.request(t, fixture.root, firstAccess, proof, true, "")
		require.Equal(t, http.StatusOK, validRecorder.Code)
		require.True(t, validResponse.Success)
	})

	for _, test := range []struct {
		name   string
		mutate func(identity *service.AuthIdentity)
	}{
		{
			name: "wrong user auth version",
			mutate: func(identity *service.AuthIdentity) {
				identity.UserAuthVersion++
			},
		},
		{
			name: "wrong session version",
			mutate: func(identity *service.AuthIdentity) {
				identity.SessionVersion++
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := setupAccountPoolExportRouteTest(t, false)
			accessToken := fixture.sessionAuth(t, fixture.root)
			identity, err := service.ParseAccessToken(accessToken)
			require.NoError(t, err)
			test.mutate(&identity)
			proof, _, err := service.IssueSecurityProof(identity, "passkey", []string{
				service.SecurityProofScopeAccountPoolCredentialsExport,
				accountPoolExportResourceScope(fixture.poolID),
			})
			require.NoError(t, err)

			recorder, response := fixture.request(t, fixture.root, accessToken, proof, true, "")

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.False(t, response.Success)
			assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
			assertAccountPoolExportNoStore(t, recorder)
		})
	}
}

func TestAccountPoolRedactedExportRemainsAvailableToOrdinaryAdmin(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)

	recorder, response := fixture.request(t, fixture.admin, fixture.admin.GetAccessToken(), "", false, "")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, response.Success)
	require.Len(t, response.Data.Accounts, 1)
	assert.NotEqual(t, fixture.credentialValue, response.Data.Accounts[0].Credentials["api_key"])
	assertAccountPoolCredentialsAbsent(t, recorder, fixture.credentialValue, fixture.otherCredential)
}

func TestAccountPoolSecretExportUsesCriticalRateLimit(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, true)
	accessToken, proof := fixture.sessionAuthWithProof(
		t,
		fixture.root,
		"passkey",
		service.SecurityProofScopeAccountPoolCredentialsExport,
		accountPoolExportResourceScope(fixture.poolID),
	)
	remoteAddr := fmt.Sprintf("192.0.2.%d:1234", accountPoolExportRouteTestCounter.Add(1))

	first, firstResponse := fixture.request(t, fixture.root, accessToken, proof, true, remoteAddr)
	require.Equal(t, http.StatusOK, first.Code)
	require.True(t, firstResponse.Success)

	second, _ := fixture.request(t, fixture.root, accessToken, proof, true, remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, second.Code)
}
