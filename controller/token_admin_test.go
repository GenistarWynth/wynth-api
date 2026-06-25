package controller

import (
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
