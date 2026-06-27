package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type accountPoolAPIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type accountPoolAPIResult[T any] struct {
	Response accountPoolAPIResponse[T]
	Raw      []byte
	Code     int
}

func setupAccountPoolAPITestDB(t *testing.T) {
	t.Helper()

	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldTranslateMessage := common.TranslateMessage
	oldCryptoSecret := common.CryptoSecret
	oldCryptoSecretStable := common.CryptoSecretStable
	oldUsingSQLite := common.UsingSQLite
	common.UsingSQLite = true

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.CryptoSecret = "account-pool-api-test-secret"
	common.CryptoSecretStable = true
	common.TranslateMessage = func(c *gin.Context, key string, args ...map[string]any) string {
		return key
	}

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.TranslateMessage = oldTranslateMessage
		common.CryptoSecret = oldCryptoSecret
		common.CryptoSecretStable = oldCryptoSecretStable
		common.UsingSQLite = oldUsingSQLite
	})

	require.NoError(t, db.AutoMigrate(
		&model.AccountPool{},
		&model.AccountPoolAccount{},
		&model.AccountPoolProxy{},
		&model.AccountPoolChannelBinding{},
		&model.Channel{},
		&model.Ability{},
		&model.User{},
		&model.Log{},
	))
	// Mirror the production SQLite migration path: GORM AutoMigrate does not reliably
	// add the not-null oauth_type column on SQLite, so run the ensure-columns helper.
	require.NoError(t, model.EnsureAccountPoolAccountColumnsSQLite())
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "admin",
		Password: "password",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
}

func accountPoolAPIRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("account-pool-api-test"))))
	router.GET("/login", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "admin")
		session.Set("role", common.RoleAdminUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		if err := session.Save(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false})
			return
		}
		c.Status(http.StatusNoContent)
	})
	group := router.Group("/api/account_pools")
	group.Use(middleware.AdminAuth())
	{
		group.GET("", ListAccountPools)
		group.POST("", CreateAccountPool)
		group.GET("/proxies", ListAccountPoolProxies)
		group.POST("/proxies", CreateAccountPoolProxy)
		group.PUT("/proxies/:proxy_id", UpdateAccountPoolProxy)
		group.DELETE("/proxies/:proxy_id", DeleteAccountPoolProxy)
		group.GET("/:id", GetAccountPool)
		group.PUT("/:id", UpdateAccountPool)
		group.DELETE("/:id", DeleteAccountPool)
		group.GET("/:id/accounts", ListAccountPoolAccounts)
		group.POST("/:id/accounts", CreateAccountPoolAccount)
		group.POST("/:id/accounts/import", ImportAccountPoolAccounts)
		group.POST("/:id/accounts/:account_id/capabilities/detect", DetectAccountPoolAccountCapability)
		group.PUT("/:id/accounts/:account_id", UpdateAccountPoolAccount)
		group.DELETE("/:id/accounts/:account_id", DeleteAccountPoolAccount)
		group.POST("/:id/capabilities/detect", DetectAccountPoolCapabilities)
		group.GET("/:id/bindings", ListAccountPoolBindings)
		group.POST("/:id/bindings", CreateAccountPoolBinding)
		group.POST("/:id/bindings/channel", CreateAccountPoolBoundChannel)
		group.PUT("/:id/bindings/:binding_id", UpdateAccountPoolBinding)
		group.DELETE("/:id/bindings/:binding_id", DeleteAccountPoolBinding)
		group.POST("/:id/bindings/:binding_id/activate", ActivateAccountPoolBinding)
		group.POST("/:id/bindings/:binding_id/disable", DisableAccountPoolBinding)
	}
	return router
}

func accountPoolAPIRequest[T any](t *testing.T, router *gin.Engine, method string, target string, body any) accountPoolAPIResult[T] {
	t.Helper()

	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		payload, err := common.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(payload)
	}

	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(http.MethodGet, "/login", nil)
	router.ServeHTTP(loginRecorder, loginRequest)
	require.Equal(t, http.StatusNoContent, loginRecorder.Code)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("New-Api-User", "1")
	for _, cookie := range loginRecorder.Result().Cookies() {
		request.AddCookie(cookie)
	}
	router.ServeHTTP(recorder, request)

	raw := recorder.Body.Bytes()
	var response accountPoolAPIResponse[T]
	require.NoError(t, common.Unmarshal(raw, &response))
	return accountPoolAPIResult[T]{
		Response: response,
		Raw:      raw,
		Code:     recorder.Code,
	}
}

func TestAccountPoolAPICreateListAndRedaction(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()

	poolResult := accountPoolAPIRequest[dto.AccountPoolResponse](t, router, http.MethodPost, "/api/account_pools", dto.AccountPoolCreateRequest{
		Name:     "pool-a",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.True(t, poolResult.Response.Success, poolResult.Response.Message)

	accountResult := accountPoolAPIRequest[dto.AccountPoolAccountResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(poolResult.Response.Data.Id)+"/accounts", dto.AccountPoolAccountCreateRequest{
		Name: "primary-key",
		Credential: dto.AccountPoolCredentialConfigRequest{
			Type:   "api_key",
			APIKey: "sk-account-secret",
		},
		TokenState: dto.AccountPoolTokenStateRequest{
			AccessToken:  "account-access-secret",
			RefreshToken: "account-refresh-secret",
			Version:      1,
		},
	})
	require.True(t, accountResult.Response.Success, accountResult.Response.Message)
	assert.True(t, accountResult.Response.Data.HasCredential)
	assert.True(t, accountResult.Response.Data.HasToken)
	assert.Equal(t, 1, accountResult.Response.Data.MaxConcurrency)

	unlimitedResult := accountPoolAPIRequest[dto.AccountPoolAccountResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(poolResult.Response.Data.Id)+"/accounts", dto.AccountPoolAccountCreateRequest{
		Name: "unlimited-key",
		Credential: dto.AccountPoolCredentialConfigRequest{
			Type:   "api_key",
			APIKey: "sk-unlimited-secret",
		},
		MaxConcurrency: common.GetPointer(0),
	})
	require.True(t, unlimitedResult.Response.Success, unlimitedResult.Response.Message)
	assert.Zero(t, unlimitedResult.Response.Data.MaxConcurrency)

	var storedAccount model.AccountPoolAccount
	require.NoError(t, model.DB.First(&storedAccount, accountResult.Response.Data.Id).Error)
	var storedUnlimitedAccount model.AccountPoolAccount
	require.NoError(t, model.DB.First(&storedUnlimitedAccount, unlimitedResult.Response.Data.Id).Error)

	listResult := accountPoolAPIRequest[[]dto.AccountPoolAccountResponse](t, router, http.MethodGet, "/api/account_pools/"+strconv.Itoa(poolResult.Response.Data.Id)+"/accounts", nil)

	require.True(t, listResult.Response.Success, listResult.Response.Message)
	require.Len(t, listResult.Response.Data, 2)
	assert.True(t, listResult.Response.Data[0].HasCredential)
	assert.True(t, listResult.Response.Data[0].HasToken)
	raw := string(listResult.Raw)
	assert.NotContains(t, raw, "sk-account-secret")
	assert.NotContains(t, raw, "sk-unlimited-secret")
	assert.NotContains(t, raw, "account-access-secret")
	assert.NotContains(t, raw, "account-refresh-secret")
	assert.NotContains(t, raw, storedAccount.CredentialConfig)
	assert.NotContains(t, raw, storedUnlimitedAccount.CredentialConfig)
	assert.NotContains(t, raw, storedAccount.TokenState)
	assert.NotContains(t, raw, "ciphertext")
	assert.NotContains(t, raw, "nonce")
	assert.NotContains(t, raw, "credential_preview")
}

func TestAccountPoolAPICreateGeminiCodeAssistOAuthType(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()

	poolResult := accountPoolAPIRequest[dto.AccountPoolResponse](t, router, http.MethodPost, "/api/account_pools", dto.AccountPoolCreateRequest{
		Name:     "gemini-ca-pool",
		Platform: model.AccountPoolPlatformGemini,
	})
	require.True(t, poolResult.Response.Success, poolResult.Response.Message)

	accountResult := accountPoolAPIRequest[dto.AccountPoolAccountResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(poolResult.Response.Data.Id)+"/accounts", dto.AccountPoolAccountCreateRequest{
		Name: "ca-account",
		Credential: dto.AccountPoolCredentialConfigRequest{
			Type:         "oauth",
			Email:        "ca@example.com",
			RefreshToken: "ca-refresh",
			OAuthType:    "code_assist",
		},
		TokenState: dto.AccountPoolTokenStateRequest{
			AccessToken: "ca-access",
		},
	})
	require.True(t, accountResult.Response.Success, accountResult.Response.Message)
	// oauth_type must be exposed in the (non-secret) account view/response.
	assert.Equal(t, service.AccountPoolGeminiOAuthTypeCodeAssist, accountResult.Response.Data.OAuthType)

	// oauth_type from the create API must persist into the plaintext column so the
	// runtime can route the account through Code Assist without decrypting secrets.
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, accountResult.Response.Data.Id).Error)
	assert.Equal(t, service.AccountPoolGeminiOAuthTypeCodeAssist, stored.OAuthType)
}

func TestAccountPoolAPIUpdateAndDeleteAccount(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)

	createResult := accountPoolAPIRequest[dto.AccountPoolAccountResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/accounts", dto.AccountPoolAccountCreateRequest{
		Name: "primary-key",
		Credential: dto.AccountPoolCredentialConfigRequest{
			Type:   "api_key",
			APIKey: "sk-account-secret",
		},
	})
	require.True(t, createResult.Response.Success, createResult.Response.Message)
	accountID := createResult.Response.Data.Id

	updateResult := accountPoolAPIRequest[dto.AccountPoolAccountResponse](t, router, http.MethodPut, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/accounts/"+strconv.Itoa(accountID), dto.AccountPoolAccountCreateRequest{
		Name:              "updated-key",
		AccountIdentifier: "account-b",
		Status:            model.AccountPoolAccountStatusDisabled,
		Priority:          10,
		Weight:            20,
		MaxConcurrency:    common.GetPointer(3),
		SupportedModels:   []string{"gpt-5"},
		ModelMapping:      map[string]string{"gpt-5": "upstream-gpt-5"},
	})

	require.True(t, updateResult.Response.Success, updateResult.Response.Message)
	assert.Equal(t, "updated-key", updateResult.Response.Data.Name)
	assert.Equal(t, "account-b", updateResult.Response.Data.AccountIdentifier)
	assert.Equal(t, model.AccountPoolAccountStatusDisabled, updateResult.Response.Data.Status)
	assert.True(t, updateResult.Response.Data.HasCredential)
	assert.NotContains(t, string(updateResult.Raw), "sk-account-secret")
	var updatedStored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&updatedStored, accountID).Error)
	updatedCredential, err := service.DecryptAccountPoolCredentialConfig(updatedStored.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, "sk-account-secret", updatedCredential.APIKey)

	deleteResult := accountPoolAPIRequest[any](t, router, http.MethodDelete, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/accounts/"+strconv.Itoa(accountID), nil)
	require.True(t, deleteResult.Response.Success, deleteResult.Response.Message)

	listResult := accountPoolAPIRequest[[]dto.AccountPoolAccountResponse](t, router, http.MethodGet, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/accounts", nil)
	require.True(t, listResult.Response.Success, listResult.Response.Message)
	assert.Empty(t, listResult.Response.Data)

	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, accountID).Error)
	assert.Equal(t, model.AccountPoolAccountStatusDeleted, stored.Status)
	assert.NotContains(t, stored.CredentialConfig, "sk-account-secret")
}

func TestAccountPoolAPIDetectAccountCapabilityDryRun(t *testing.T) {
	withAccountPoolAPIFetchSetting(t, true)
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	accountPoolService := service.AccountPoolService{}

	account, err := accountPoolService.CreateAccount(service.AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "dry-run-account",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-dry-run",
		},
		SupportedModels: []string{"existing"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "Bearer sk-dry-run", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, writeErr := w.Write([]byte(`{"data":[{"id":"gpt-5"},{"id":"gpt-5-mini"},{"id":""},{"id":"gpt-5"}]}`))
		require.NoError(t, writeErr)
	}))
	defer server.Close()

	channel := createAccountPoolAPITestChannelWithBaseURL(t, common.ChannelStatusManuallyDisabled, server.URL)
	_, err = accountPoolService.CreateBinding(service.AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	result := accountPoolAPIRequest[dto.AccountPoolCapabilityDetectResult](
		t,
		router,
		http.MethodPost,
		"/api/account_pools/"+strconv.Itoa(pool.Id)+"/accounts/"+strconv.Itoa(account.Id)+"/capabilities/detect",
		dto.AccountPoolCapabilityDetectRequest{
			Mode:            "models_endpoint",
			ChannelID:       channel.Id,
			CandidateModels: []string{"gpt-5-mini", "missing"},
			Apply:           false,
		},
	)

	require.Equal(t, http.StatusOK, result.Code, string(result.Raw))
	require.True(t, result.Response.Success, result.Response.Message)
	assert.Equal(t, account.Id, result.Response.Data.AccountID)
	assert.Equal(t, "success", result.Response.Data.Status)
	assert.Equal(t, "models_endpoint", result.Response.Data.Mode)
	assert.Equal(t, []string{"gpt-5-mini"}, result.Response.Data.DetectedModels)
	assert.Empty(t, result.Response.Data.AppliedModels)
	require.NotNil(t, result.Response.Data.ModelMapping)
	assert.Empty(t, result.Response.Data.ModelMapping)
	require.NotNil(t, result.Response.Data.Errors)
	assert.Empty(t, result.Response.Data.Errors)

	stored := loadAccountPoolAPITestAccount(t, account.Id)
	assert.Equal(t, []string{"existing"}, mustUnmarshalAccountPoolAPITestModels(t, stored.SupportedModels))
	assert.Zero(t, stored.LastCapabilityCheckAt)
	assert.Empty(t, stored.LastCapabilityCheckStatus)
	assert.Empty(t, stored.LastCapabilityCheckError)
	assert.Empty(t, stored.LastCapabilityCheckModels)
}

func TestAccountPoolAPIDetectPoolCapabilitiesContinuesAfterFailure(t *testing.T) {
	withAccountPoolAPIFetchSetting(t, true)
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	accountPoolService := service.AccountPoolService{}

	successAccount, err := accountPoolService.CreateAccount(service.AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "success-account",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-success",
		},
		SupportedModels: []string{"existing-success"},
	})
	require.NoError(t, err)

	failedAccount, err := accountPoolService.CreateAccount(service.AccountPoolAccountCreateParams{
		PoolID: pool.Id,
		Name:   "failed-account",
		Credential: service.AccountPoolCredentialConfig{
			Type:   service.AccountPoolCredentialTypeAPIKey,
			APIKey: "sk-fail",
		},
		SupportedModels: []string{"existing-fail"},
	})
	require.NoError(t, err)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v1/models", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("Authorization") {
		case "Bearer sk-success":
			_, writeErr := w.Write([]byte(`{"data":[{"id":"gpt-5"}]}`))
			require.NoError(t, writeErr)
		case "Bearer sk-fail":
			w.WriteHeader(http.StatusUnauthorized)
			_, writeErr := w.Write([]byte(`{"error":{"message":"bearer sk-fail rejected"}}`))
			require.NoError(t, writeErr)
		default:
			t.Fatalf("unexpected authorization header %q", r.Header.Get("Authorization"))
		}
	}))
	defer server.Close()

	channel := createAccountPoolAPITestChannelWithBaseURL(t, common.ChannelStatusManuallyDisabled, server.URL)
	_, err = accountPoolService.CreateBinding(service.AccountPoolBindingCreateParams{
		PoolID:    pool.Id,
		ChannelID: channel.Id,
	})
	require.NoError(t, err)

	result := accountPoolAPIRequest[dto.AccountPoolCapabilityPoolResult](
		t,
		router,
		http.MethodPost,
		"/api/account_pools/"+strconv.Itoa(pool.Id)+"/capabilities/detect",
		dto.AccountPoolCapabilityDetectRequest{
			Mode:       "models_endpoint",
			ChannelID:  channel.Id,
			AccountIDs: []int{successAccount.Id, failedAccount.Id},
			Apply:      true,
		},
	)

	require.Equal(t, http.StatusOK, result.Code, string(result.Raw))
	require.True(t, result.Response.Success, result.Response.Message)
	assert.Equal(t, 2, result.Response.Data.Total)
	assert.Equal(t, 1, result.Response.Data.Succeeded)
	assert.Equal(t, 1, result.Response.Data.Failed)
	require.Len(t, result.Response.Data.Results, 2)

	resultsByAccountID := make(map[int]dto.AccountPoolCapabilityDetectResult, len(result.Response.Data.Results))
	for _, item := range result.Response.Data.Results {
		resultsByAccountID[item.AccountID] = item
	}

	successResult, ok := resultsByAccountID[successAccount.Id]
	require.True(t, ok)
	assert.Equal(t, "success", successResult.Status)
	assert.Equal(t, []string{"gpt-5"}, successResult.DetectedModels)
	assert.Equal(t, []string{"gpt-5"}, successResult.AppliedModels)
	require.NotNil(t, successResult.Errors)
	assert.Empty(t, successResult.Errors)

	failedResult, ok := resultsByAccountID[failedAccount.Id]
	require.True(t, ok)
	assert.Equal(t, "auth_error", failedResult.Status)
	assert.Empty(t, failedResult.DetectedModels)
	assert.Empty(t, failedResult.AppliedModels)
	require.NotEmpty(t, failedResult.Errors)
	assert.NotContains(t, failedResult.Errors[0], "sk-fail")

	successStored := loadAccountPoolAPITestAccount(t, successAccount.Id)
	assert.Equal(t, []string{"gpt-5"}, mustUnmarshalAccountPoolAPITestModels(t, successStored.SupportedModels))
	assert.Equal(t, "success", successStored.LastCapabilityCheckStatus)

	failedStored := loadAccountPoolAPITestAccount(t, failedAccount.Id)
	assert.Equal(t, []string{"existing-fail"}, mustUnmarshalAccountPoolAPITestModels(t, failedStored.SupportedModels))
	assert.Equal(t, "auth_error", failedStored.LastCapabilityCheckStatus)
	assert.NotContains(t, failedStored.LastCapabilityCheckError, "sk-fail")
}

func TestAccountPoolAPIImportAccountsRedactsSecrets(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)

	result := accountPoolAPIRequest[dto.AccountPoolAccountImportResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/accounts/import", dto.AccountPoolAccountImportRequest{
		Format: "sub2api",
		Content: `{
			"type": "sub2api-data",
			"accounts": [
				{
					"name": "imported-key",
					"platform": "openai",
					"type": "api_key",
					"credentials": {
						"api_key": "sk-import-secret"
					}
				}
			]
		}`,
		Defaults: dto.AccountPoolAccountImportDefaultsRequest{
			MaxConcurrency: common.GetPointer(0),
		},
	})

	require.True(t, result.Response.Success, result.Response.Message)
	assert.Equal(t, 1, result.Response.Data.Imported)
	require.Len(t, result.Response.Data.Accounts, 1)
	assert.Zero(t, result.Response.Data.Accounts[0].MaxConcurrency)
	assert.True(t, result.Response.Data.Accounts[0].HasCredential)
	raw := string(result.Raw)
	assert.NotContains(t, raw, "sk-import-secret")
	assert.NotContains(t, raw, "ciphertext")
	assert.NotContains(t, raw, "nonce")

	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, result.Response.Data.Accounts[0].Id).Error)
	assert.NotContains(t, stored.CredentialConfig, "sk-import-secret")
	credential, err := service.DecryptAccountPoolCredentialConfig(stored.CredentialConfig)
	require.NoError(t, err)
	assert.Equal(t, "sk-import-secret", credential.APIKey)
}

func TestAccountPoolAPIBindingRejectsEnabledChannel(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	channel := createAccountPoolAPITestChannel(t, common.ChannelStatusEnabled)

	result := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: channel.Id,
	})

	require.False(t, result.Response.Success)
	assert.Contains(t, result.Response.Message, "disabled channel")
}

func TestAccountPoolAPIBindingCreatesDraftForDisabledChannel(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	channel := createAccountPoolAPITestChannel(t, common.ChannelStatusManuallyDisabled)

	result := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: channel.Id,
	})

	require.True(t, result.Response.Success, result.Response.Message)
	assert.Equal(t, model.AccountPoolBindingStatusDraft, result.Response.Data.Status)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, result.Response.Data.ChannelStatus)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)
}

func TestAccountPoolAPICreateBoundChannelCreatesDisabledChannel(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)

	result := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings/channel", dto.AccountPoolBoundChannelCreateRequest{
		Name:              "  Pool runtime channel  ",
		AccountRetryTimes: 2,
	})

	require.Equal(t, http.StatusOK, result.Code, string(result.Raw))
	assert.True(t, result.Response.Success)
	assert.Equal(t, model.AccountPoolBindingStatusDraft, result.Response.Data.Status)
	assert.Equal(t, "Pool runtime channel", result.Response.Data.ChannelName)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, result.Response.Data.ChannelStatus)
	assert.Equal(t, 2, result.Response.Data.AccountRetryTimes)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, result.Response.Data.ChannelID).Error)
	assert.Equal(t, "Pool runtime channel", channel.Name)
	assert.Equal(t, constant.ChannelTypeOpenAI, channel.Type)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, channel.Status)
	assert.NotEmpty(t, channel.Key)
}

func TestAccountPoolAPIBindingActivateDisableControlsRuntimeButNotChannel(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	channel := createAccountPoolAPITestChannel(t, common.ChannelStatusManuallyDisabled)
	createResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: channel.Id,
	})
	require.True(t, createResult.Response.Success, createResult.Response.Message)

	activateResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings/"+strconv.Itoa(createResult.Response.Data.Id)+"/activate", nil)

	require.True(t, activateResult.Response.Success, activateResult.Response.Message)
	assert.Equal(t, model.AccountPoolBindingStatusEnabled, activateResult.Response.Data.Status)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, activateResult.Response.Data.ChannelStatus)
	assert.True(t, activateResult.Response.Data.RuntimeEnabled)
	var reloaded model.Channel
	require.NoError(t, model.DB.First(&reloaded, channel.Id).Error)
	assert.Equal(t, common.ChannelStatusManuallyDisabled, reloaded.Status)
	enabled, err := service.AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	assert.True(t, enabled)

	disableResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings/"+strconv.Itoa(createResult.Response.Data.Id)+"/disable", nil)

	require.True(t, disableResult.Response.Success, disableResult.Response.Message)
	assert.Equal(t, model.AccountPoolBindingStatusDisabled, disableResult.Response.Data.Status)
	assert.False(t, disableResult.Response.Data.RuntimeEnabled)
	enabled, err = service.AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	assert.False(t, enabled)
}

func TestAccountPoolAPIDeleteBindingReleasesChannel(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	channel := createAccountPoolAPITestChannel(t, common.ChannelStatusManuallyDisabled)
	createResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: channel.Id,
	})
	require.True(t, createResult.Response.Success, createResult.Response.Message)
	activateResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings/"+strconv.Itoa(createResult.Response.Data.Id)+"/activate", nil)
	require.True(t, activateResult.Response.Success, activateResult.Response.Message)
	enabled, err := service.AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	require.True(t, enabled)

	deleteResult := accountPoolAPIRequest[any](t, router, http.MethodDelete, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings/"+strconv.Itoa(createResult.Response.Data.Id), nil)

	require.True(t, deleteResult.Response.Success, deleteResult.Response.Message)
	enabled, err = service.AccountPoolRuntimeEnabledForChannel(channel.Id)
	require.NoError(t, err)
	assert.False(t, enabled)
	var reloadedBinding model.AccountPoolChannelBinding
	require.Error(t, model.DB.First(&reloadedBinding, createResult.Response.Data.Id).Error)
	rebindResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: channel.Id,
	})
	require.True(t, rebindResult.Response.Success, rebindResult.Response.Message)
	assert.Equal(t, channel.Id, rebindResult.Response.Data.ChannelID)
}

func TestAccountPoolAPIUpdateBindingConfig(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	oldChannel := createAccountPoolAPITestChannel(t, common.ChannelStatusManuallyDisabled)
	newChannel := createAccountPoolAPITestChannel(t, common.ChannelStatusManuallyDisabled)
	createResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: oldChannel.Id,
	})
	require.True(t, createResult.Response.Success, createResult.Response.Message)
	activateResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings/"+strconv.Itoa(createResult.Response.Data.Id)+"/activate", nil)
	require.True(t, activateResult.Response.Success, activateResult.Response.Message)

	updateResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPut, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings/"+strconv.Itoa(createResult.Response.Data.Id), dto.AccountPoolBindingCreateRequest{
		ChannelID:         newChannel.Id,
		AccountIDs:        []int{11, 22},
		ModelStrategy:     "fixed",
		FixedModels:       []string{"gpt-5", "gpt-5-mini"},
		SchedulePolicy:    "random",
		AccountRetryTimes: 2,
	})

	require.True(t, updateResult.Response.Success, updateResult.Response.Message)
	assert.Equal(t, model.AccountPoolBindingStatusEnabled, updateResult.Response.Data.Status)
	assert.Equal(t, newChannel.Id, updateResult.Response.Data.ChannelID)
	assert.Equal(t, "random", updateResult.Response.Data.SchedulePolicy)
	assert.Equal(t, 2, updateResult.Response.Data.AccountRetryTimes)
	var filter service.AccountPoolAccountFilterConfig
	require.NoError(t, common.UnmarshalJsonStr(updateResult.Response.Data.AccountFilterConfig, &filter))
	assert.Equal(t, []int{11, 22}, filter.AccountIDs)
	var policy service.AccountPoolModelPolicy
	require.NoError(t, common.UnmarshalJsonStr(updateResult.Response.Data.ModelPolicy, &policy))
	assert.Equal(t, service.AccountPoolModelPolicy{
		Strategy:    "fixed",
		FixedModels: []string{"gpt-5", "gpt-5-mini"},
	}, policy)
}

func TestAccountPoolAPIBindingActivationRejectsWrongPoolAndUnsupportedChannel(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()
	pool := createAccountPoolAPITestPool(t, router)
	otherPool := createAccountPoolAPITestPool(t, router)
	channel := createAccountPoolAPITestChannel(t, common.ChannelStatusManuallyDisabled)
	createResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: channel.Id,
	})
	require.True(t, createResult.Response.Success, createResult.Response.Message)

	wrongPoolResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(otherPool.Id)+"/bindings/"+strconv.Itoa(createResult.Response.Data.Id)+"/activate", nil)

	require.False(t, wrongPoolResult.Response.Success)

	unsupported := createAccountPoolAPITestChannelWithType(t, constant.ChannelTypeMidjourney, common.ChannelStatusManuallyDisabled)
	unsupportedResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{
		ChannelID: unsupported.Id,
	})

	require.False(t, unsupportedResult.Response.Success)
	assert.Contains(t, unsupportedResult.Response.Message, "OpenAI-compatible")
}

func TestAccountPoolAPIProxyRedaction(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()

	createResult := accountPoolAPIRequest[dto.AccountPoolProxyResponse](t, router, http.MethodPost, "/api/account_pools/proxies", dto.AccountPoolProxyCreateRequest{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
		Username: "proxy-user",
		Password: "proxy-password-secret",
	})

	require.True(t, createResult.Response.Success, createResult.Response.Message)
	assert.True(t, createResult.Response.Data.HasPassword)
	assert.NotContains(t, string(createResult.Raw), "proxy-password-secret")
	var stored model.AccountPoolProxy
	require.NoError(t, model.DB.First(&stored, createResult.Response.Data.Id).Error)
	assert.NotContains(t, stored.Password, "proxy-password-secret")

	listResult := accountPoolAPIRequest[[]dto.AccountPoolProxyResponse](t, router, http.MethodGet, "/api/account_pools/proxies", nil)

	require.True(t, listResult.Response.Success, listResult.Response.Message)
	require.Len(t, listResult.Response.Data, 1)
	assert.True(t, listResult.Response.Data[0].HasPassword)
	raw := string(listResult.Raw)
	assert.NotContains(t, raw, "proxy-password-secret")
	assert.NotContains(t, raw, stored.Password)
	assert.NotContains(t, raw, "ciphertext")
	assert.NotContains(t, raw, "nonce")
}

func TestAccountPoolAPIUpdateAndDeleteProxy(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()

	createResult := accountPoolAPIRequest[dto.AccountPoolProxyResponse](t, router, http.MethodPost, "/api/account_pools/proxies", dto.AccountPoolProxyCreateRequest{
		Name:     "proxy-a",
		Protocol: "http",
		Host:     "127.0.0.1",
		Port:     8080,
		Username: "proxy-user",
		Password: "proxy-password-secret",
	})
	require.True(t, createResult.Response.Success, createResult.Response.Message)

	updateResult := accountPoolAPIRequest[dto.AccountPoolProxyResponse](t, router, http.MethodPut, "/api/account_pools/proxies/"+strconv.Itoa(createResult.Response.Data.Id), dto.AccountPoolProxyCreateRequest{
		Name:     "proxy-updated",
		Protocol: "socks5",
		Host:     "10.0.0.1",
		Port:     1080,
		Username: "proxy-user-updated",
		Status:   model.AccountPoolProxyStatusDisabled,
	})

	require.True(t, updateResult.Response.Success, updateResult.Response.Message)
	assert.Equal(t, "proxy-updated", updateResult.Response.Data.Name)
	assert.Equal(t, "socks5", updateResult.Response.Data.Protocol)
	assert.Equal(t, "10.0.0.1", updateResult.Response.Data.Host)
	assert.Equal(t, 1080, updateResult.Response.Data.Port)
	assert.Equal(t, "proxy-user-updated", updateResult.Response.Data.Username)
	assert.Equal(t, model.AccountPoolProxyStatusDisabled, updateResult.Response.Data.Status)
	assert.True(t, updateResult.Response.Data.HasPassword)
	assert.NotContains(t, string(updateResult.Raw), "proxy-password-secret")

	deleteResult := accountPoolAPIRequest[any](t, router, http.MethodDelete, "/api/account_pools/proxies/"+strconv.Itoa(createResult.Response.Data.Id), nil)
	require.True(t, deleteResult.Response.Success, deleteResult.Response.Message)

	listResult := accountPoolAPIRequest[[]dto.AccountPoolProxyResponse](t, router, http.MethodGet, "/api/account_pools/proxies", nil)
	require.True(t, listResult.Response.Success, listResult.Response.Message)
	assert.Empty(t, listResult.Response.Data)
}

func TestAccountPoolAPIRejectsMissingRequiredFields(t *testing.T) {
	setupAccountPoolAPITestDB(t)
	router := accountPoolAPIRouter()

	poolResult := accountPoolAPIRequest[dto.AccountPoolResponse](t, router, http.MethodPost, "/api/account_pools", dto.AccountPoolCreateRequest{})
	require.False(t, poolResult.Response.Success)
	assert.Contains(t, poolResult.Response.Message, "Name")

	pool := createAccountPoolAPITestPool(t, router)
	bindingResult := accountPoolAPIRequest[dto.AccountPoolBindingResponse](t, router, http.MethodPost, "/api/account_pools/"+strconv.Itoa(pool.Id)+"/bindings", dto.AccountPoolBindingCreateRequest{})
	require.False(t, bindingResult.Response.Success)
	assert.Contains(t, bindingResult.Response.Message, "ChannelID")
}

func createAccountPoolAPITestPool(t *testing.T, router *gin.Engine) dto.AccountPoolResponse {
	t.Helper()

	result := accountPoolAPIRequest[dto.AccountPoolResponse](t, router, http.MethodPost, "/api/account_pools", dto.AccountPoolCreateRequest{
		Name:     "pool-a",
		Platform: model.AccountPoolPlatformOpenAI,
	})
	require.True(t, result.Response.Success, result.Response.Message)
	return result.Response.Data
}

func createAccountPoolAPITestChannel(t *testing.T, status int) model.Channel {
	t.Helper()

	return createAccountPoolAPITestChannelWithType(t, constant.ChannelTypeOpenAI, status)
}

func createAccountPoolAPITestChannelWithBaseURL(t *testing.T, status int, baseURL string) model.Channel {
	t.Helper()

	channel := createAccountPoolAPITestChannel(t, status)
	channel.BaseURL = common.GetPointer(baseURL)
	require.NoError(t, model.DB.Model(&model.Channel{}).
		Where("id = ?", channel.Id).
		Update("base_url", baseURL).Error)
	return channel
}

func createAccountPoolAPITestChannelWithType(t *testing.T, channelType int, status int) model.Channel {
	t.Helper()

	channel := model.Channel{
		Type:   channelType,
		Key:    "sk-channel",
		Name:   "channel-a",
		Status: status,
	}
	require.NoError(t, model.DB.Create(&channel).Error)
	return channel
}

func withAccountPoolAPIFetchSetting(t *testing.T, allowPrivate bool) {
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

func loadAccountPoolAPITestAccount(t *testing.T, accountID int) model.AccountPoolAccount {
	t.Helper()

	var account model.AccountPoolAccount
	require.NoError(t, model.DB.First(&account, accountID).Error)
	return account
}

func mustUnmarshalAccountPoolAPITestModels(t *testing.T, raw string) []string {
	t.Helper()

	var models []string
	require.NoError(t, common.UnmarshalJsonStr(raw, &models))
	return models
}
