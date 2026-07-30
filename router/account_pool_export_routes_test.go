package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var accountPoolExportRouteTestCounter atomic.Uint32

type accountPoolExportRouteFixture struct {
	engine          *gin.Engine
	poolID          int
	credentialValue string
	root            model.User
	admin           model.User
}

type accountPoolExportRouteResponse struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Data    struct {
		Accounts []struct {
			Credentials map[string]any `json:"credentials"`
		} `json:"accounts"`
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
	dsn := fmt.Sprintf("file:account-pool-export-routes-%d?mode=memory&cache=shared", testID)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

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

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	SetApiRouter(engine)
	return accountPoolExportRouteFixture{
		engine:          engine,
		poolID:          pool.Id,
		credentialValue: credentialValue,
		root:            root,
		admin:           admin,
	}
}

func (fixture accountPoolExportRouteFixture) sessionAuth(t *testing.T, user model.User, proofScope string) (string, string) {
	t.Helper()
	bundle, err := service.CreateLoginSession(user.Id, "password", "127.0.0.1", "account-pool-export-route-test")
	require.NoError(t, err)
	if proofScope == "" {
		return bundle.AccessToken, ""
	}
	identity, err := service.ParseAccessToken(bundle.AccessToken)
	require.NoError(t, err)
	proof, _, err := service.IssueSecurityProof(identity, "2fa", []string{proofScope})
	require.NoError(t, err)
	return bundle.AccessToken, proof
}

func (fixture accountPoolExportRouteFixture) request(t *testing.T, user model.User, accessToken string, proof string, includeSecrets bool, remoteAddr string) (*httptest.ResponseRecorder, accountPoolExportRouteResponse) {
	t.Helper()
	target := fmt.Sprintf("/api/account_pools/%d/accounts/export", fixture.poolID)
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
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	}
	return recorder, response
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
				return fixture.sessionAuth(t, fixture.admin, service.SecurityProofScopeAccountPoolCredentialsExport)
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
			assert.False(t, strings.Contains(recorder.Body.String(), fixture.credentialValue))
		})
	}
}

func TestAccountPoolSecretExportRejectsRootWithoutActionProof(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)
	accessToken, _ := fixture.sessionAuth(t, fixture.root, "")

	recorder, response := fixture.request(t, fixture.root, accessToken, "", true, "")

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, "SECURITY_PROOF_REQUIRED", response.Code)
	assert.False(t, strings.Contains(recorder.Body.String(), fixture.credentialValue))
}

func TestAccountPoolSecretExportRejectsRootPAT(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)
	_, proof := fixture.sessionAuth(t, fixture.root, service.SecurityProofScopeAccountPoolCredentialsExport)

	recorder, response := fixture.request(t, fixture.root, fixture.root.GetAccessToken(), proof, true, "")

	assert.Equal(t, http.StatusForbidden, recorder.Code)
	assert.Equal(t, "SECURITY_PROOF_INVALID", response.Code)
	assert.False(t, strings.Contains(recorder.Body.String(), fixture.credentialValue))
}

func TestAccountPoolSecretExportRequiresActionSpecificProofAndDisablesCaching(t *testing.T) {
	t.Run("channel proof is rejected", func(t *testing.T) {
		fixture := setupAccountPoolExportRouteTest(t, false)
		accessToken, proof := fixture.sessionAuth(t, fixture.root, "channel.key.read")

		recorder, response := fixture.request(t, fixture.root, accessToken, proof, true, "")

		assert.Equal(t, http.StatusForbidden, recorder.Code)
		assert.Equal(t, "SECURITY_PROOF_SCOPE_MISMATCH", response.Code)
		assert.False(t, strings.Contains(recorder.Body.String(), fixture.credentialValue))
	})

	t.Run("account pool credentials proof succeeds", func(t *testing.T) {
		fixture := setupAccountPoolExportRouteTest(t, false)
		accessToken, proof := fixture.sessionAuth(t, fixture.root, service.SecurityProofScopeAccountPoolCredentialsExport)

		recorder, response := fixture.request(t, fixture.root, accessToken, proof, true, "")

		require.Equal(t, http.StatusOK, recorder.Code)
		require.True(t, response.Success)
		require.Len(t, response.Data.Accounts, 1)
		assert.Equal(t, fixture.credentialValue, response.Data.Accounts[0].Credentials["api_key"])
		assert.Equal(t, "no-store, no-cache, must-revalidate, private, max-age=0", recorder.Header().Get("Cache-Control"))
		assert.Equal(t, "no-cache", recorder.Header().Get("Pragma"))
		assert.Equal(t, "0", recorder.Header().Get("Expires"))
	})
}

func TestAccountPoolRedactedExportRemainsAvailableToOrdinaryAdmin(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, false)

	recorder, response := fixture.request(t, fixture.admin, fixture.admin.GetAccessToken(), "", false, "")

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, response.Success)
	require.Len(t, response.Data.Accounts, 1)
	assert.NotEqual(t, fixture.credentialValue, response.Data.Accounts[0].Credentials["api_key"])
	assert.False(t, strings.Contains(recorder.Body.String(), fixture.credentialValue))
}

func TestAccountPoolSecretExportUsesCriticalRateLimit(t *testing.T) {
	fixture := setupAccountPoolExportRouteTest(t, true)
	accessToken, proof := fixture.sessionAuth(t, fixture.root, service.SecurityProofScopeAccountPoolCredentialsExport)
	remoteAddr := fmt.Sprintf("192.0.2.%d:1234", accountPoolExportRouteTestCounter.Add(1))

	first, firstResponse := fixture.request(t, fixture.root, accessToken, proof, true, remoteAddr)
	require.Equal(t, http.StatusOK, first.Code)
	require.True(t, firstResponse.Success)

	second, _ := fixture.request(t, fixture.root, accessToken, proof, true, remoteAddr)
	assert.Equal(t, http.StatusTooManyRequests, second.Code)
}
