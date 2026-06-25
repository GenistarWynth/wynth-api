package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupTokenAdminTestDB reuses the model-list in-memory SQLite fixture and adds
// the Token and Log tables this feature touches.
func setupTokenAdminTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}, &model.Log{}))
	return db
}

// helper: build an admin-authenticated test context targeting user :id = targetId
func adminTokenCtx(method, url string, actorRole, targetId int) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, url, nil)
	c.Params = gin.Params{{Key: "id", Value: itoa(targetId)}}
	c.Set("id", 1)           // actor (admin) id
	c.Set("role", actorRole) // actor role
	return c, rec
}

func itoa(i int) string { return strconv.Itoa(i) }

func seedUsersAndTokens(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Create(&model.User{Id: 1, Username: "admin", AffCode: "a1", Group: "default", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.User{Id: 2, Username: "alice", AffCode: "a2", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.User{Id: 3, Username: "bob", AffCode: "a3", Group: "default", Role: common.RoleCommonUser, Status: common.UserStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 10, UserId: 2, Name: "alice-key", Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Status: common.TokenStatusEnabled}).Error)
	require.NoError(t, db.Create(&model.Token{Id: 20, UserId: 3, Name: "bob-key", Key: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Status: common.TokenStatusEnabled}).Error)
}

func TestAdminGetUserTokens_ScopesToTarget(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)

	c, rec := adminTokenCtx(http.MethodGet, "/api/user/2/tokens", common.RoleAdminUser, 2)
	AdminGetUserTokens(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Items []model.Token `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	require.Len(t, resp.Data.Items, 1)
	assert.Equal(t, 10, resp.Data.Items[0].Id)
	assert.NotEqual(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", resp.Data.Items[0].Key, "key must be masked")
}

func TestAdminGetUserTokens_DeniesSameOrHigherRole(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	// promote target user 2 to admin → actor admin (role 10) must NOT manage a peer
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 2).Update("role", common.RoleAdminUser).Error)

	c, rec := adminTokenCtx(http.MethodGet, "/api/user/2/tokens", common.RoleAdminUser, 2)
	AdminGetUserTokens(c)

	var resp struct{ Success bool `json:"success"` }
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "admin must not manage a same-level user's tokens")
}

func TestAdminGetUserToken_Single(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)

	c, rec := adminTokenCtx(http.MethodGet, "/api/user/2/tokens/10", common.RoleAdminUser, 2)
	c.Params = gin.Params{{Key: "id", Value: "2"}, {Key: "tid", Value: "10"}}
	AdminGetUserToken(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Success bool        `json:"success"`
		Data    model.Token `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	assert.Equal(t, 10, resp.Data.Id)
}

func TestValidateTokenWriteInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name  string
		token model.Token
		ok    bool
	}{
		{"valid limited", model.Token{Name: "ok", RemainQuota: 100}, true},
		{"valid unlimited negative ignored", model.Token{Name: "ok", UnlimitedQuota: true, RemainQuota: -5}, true},
		{"name too long", model.Token{Name: string(make([]byte, 51))}, false},
		{"negative quota", model.Token{Name: "ok", RemainQuota: -1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			tok := tc.token
			got := validateTokenWriteInput(c, &tok)
			assert.Equal(t, tc.ok, got)
		})
	}
}

func putJSONCtx(t *testing.T, url string, actorRole, targetId int, body any) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	raw, err := common.Marshal(body)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, url, bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: itoa(targetId)}}
	c.Set("id", 1)
	c.Set("role", actorRole)
	return c, rec
}

func TestAdminUpdateUserToken_FullObjectPreservesFields(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	// give alice's token an expiry + model limits so we can confirm a full update keeps them
	require.NoError(t, db.Model(&model.Token{}).Where("id = ?", 10).
		Updates(map[string]any{"expired_time": int64(9999999999), "remain_quota": 500}).Error)

	c, rec := putJSONCtx(t, "/api/user/2/tokens", common.RoleAdminUser, 2, model.Token{
		Id: 10, Name: "renamed", ExpiredTime: 9999999999, RemainQuota: 500,
	})
	AdminUpdateUserToken(c)
	require.Equal(t, http.StatusOK, rec.Code)

	var got model.Token
	require.NoError(t, db.First(&got, 10).Error)
	assert.Equal(t, "renamed", got.Name)
	assert.Equal(t, int64(9999999999), got.ExpiredTime)
	assert.Equal(t, 500, got.RemainQuota)

	// user account quota untouched
	var alice model.User
	require.NoError(t, db.First(&alice, 2).Error)
	assert.Equal(t, 0, alice.Quota)
}

func TestAdminUpdateUserToken_StatusOnly(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)

	c, rec := putJSONCtx(t, "/api/user/2/tokens?status_only=true", common.RoleAdminUser, 2, model.Token{
		Id: 10, Status: common.TokenStatusDisabled,
	})
	c.Request.URL.RawQuery = "status_only=true"
	AdminUpdateUserToken(c)
	require.Equal(t, http.StatusOK, rec.Code)

	var got model.Token
	require.NoError(t, db.First(&got, 10).Error)
	assert.Equal(t, common.TokenStatusDisabled, got.Status)
}

func TestAdminUpdateUserToken_RejectsUnusableGroupForCommonTarget(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	withUserSelectableGroupFixture(t) // "admin_only" exists but is not user-selectable

	c, rec := putJSONCtx(t, "/api/user/2/tokens", common.RoleAdminUser, 2, model.Token{
		Id: 10, Name: "alice-key", Group: "admin_only",
	})
	AdminUpdateUserToken(c)

	var resp struct{ Success bool `json:"success"` }
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "must reject a group the common target cannot use")
}

func TestAdminDeleteUserToken_ScopedToTarget(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)

	// Attempt to delete bob's token (id 20) via alice's path (target id 2) → must not delete.
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/user/2/tokens/20", nil)
	c.Params = gin.Params{{Key: "id", Value: "2"}, {Key: "tid", Value: "20"}}
	c.Set("id", 1)
	c.Set("role", common.RoleAdminUser)
	AdminDeleteUserToken(c)

	var bobTok model.Token
	assert.NoError(t, db.First(&bobTok, 20).Error, "bob's token must survive a cross-user delete attempt")

	// Deleting alice's own token (id 10) succeeds.
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodDelete, "/api/user/2/tokens/10", nil)
	c2.Params = gin.Params{{Key: "id", Value: "2"}, {Key: "tid", Value: "10"}}
	c2.Set("id", 1)
	c2.Set("role", common.RoleAdminUser)
	AdminDeleteUserToken(c2)
	require.Equal(t, http.StatusOK, rec2.Code)
	assert.Error(t, db.First(&model.Token{}, 10).Error, "alice's token should be deleted")
}

func TestAdminBatchDeleteUserTokens(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	require.NoError(t, db.Create(&model.Token{Id: 11, UserId: 2, Name: "alice-key-2", Key: "cccccccccccccccccccccccccccccccc", Status: common.TokenStatusEnabled}).Error)

	body, _ := common.Marshal(TokenBatch{Ids: []int{10, 11, 20}}) // 20 belongs to bob, must be ignored
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/2/tokens/batch", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "2"}}
	c.Set("id", 1)
	c.Set("role", common.RoleAdminUser)
	AdminBatchDeleteUserTokens(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp struct {
		Success bool `json:"success"`
		Data    int  `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, 2, resp.Data, "only the two target-owned tokens are deleted")
	assert.NoError(t, db.First(&model.Token{}, 20).Error, "bob's token untouched")
}

func TestAdminCreateUserToken_NoPlaintextInResponse(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)

	body, _ := common.Marshal(model.Token{Name: "created-by-admin", RemainQuota: 100})
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/2/tokens", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "2"}}
	c.Set("id", 1)
	c.Set("role", common.RoleAdminUser)
	AdminCreateUserToken(c)

	require.Equal(t, http.StatusOK, rec.Code)
	// The full generated key must NOT appear anywhere in the response body.
	assert.NotContains(t, rec.Body.String(), "sk-")
	var created model.Token
	require.NoError(t, db.Where("user_id = ? AND name = ?", 2, "created-by-admin").First(&created).Error)
	assert.NotEmpty(t, created.Key, "token persisted with a real key")
	assert.NotContains(t, rec.Body.String(), created.Key, "plaintext key must not be returned")
}

func revealCtx(actorRole, targetId, tid int) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/"+itoa(targetId)+"/tokens/"+itoa(tid)+"/key", nil)
	c.Params = gin.Params{{Key: "id", Value: itoa(targetId)}, {Key: "tid", Value: itoa(tid)}}
	c.Set("id", 1)
	c.Set("role", actorRole)
	return c, rec
}

func TestAdminGetUserTokenKey_RootOnly(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	// actor must outrank/own; set actor 1 to root for the allow case
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("role", common.RoleRootUser).Error)

	// Non-root admin → rejected by in-handler check.
	cAdmin, recAdmin := revealCtx(common.RoleAdminUser, 2, 10)
	AdminGetUserTokenKey(cAdmin)
	var adminResp struct{ Success bool `json:"success"` }
	require.NoError(t, common.Unmarshal(recAdmin.Body.Bytes(), &adminResp))
	assert.False(t, adminResp.Success, "admins must not reveal plaintext keys")

	// Root → full key returned.
	cRoot, recRoot := revealCtx(common.RoleRootUser, 2, 10)
	AdminGetUserTokenKey(cRoot)
	require.Equal(t, http.StatusOK, recRoot.Code)
	var rootResp struct {
		Success bool `json:"success"`
		Data    struct {
			Key string `json:"key"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recRoot.Body.Bytes(), &rootResp))
	require.True(t, rootResp.Success)
	assert.Equal(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", rootResp.Data.Key)
}

func TestAdminGetUserTokenKey_ScopedToTarget(t *testing.T) {
	db := setupTokenAdminTestDB(t)
	seedUsersAndTokens(t, db)
	require.NoError(t, db.Model(&model.User{}).Where("id = ?", 1).Update("role", common.RoleRootUser).Error)

	// Try to read bob's token (20) via alice's path (target 2) → not found, no leak.
	c, rec := revealCtx(common.RoleRootUser, 2, 20)
	AdminGetUserTokenKey(c)
	var resp struct{ Success bool `json:"success"` }
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	assert.False(t, resp.Success, "cross-user token id must not resolve")
}
