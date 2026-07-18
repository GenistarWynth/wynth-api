package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTokenAuthReadOnlyObservableTokenStates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Token{}))
	model.DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	model.InitCommonColumnsForTest()
	common.RedisEnabled = false

	user := model.User{Username: "readonly-user", Password: "password", Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, db.Create(&user).Error)

	tests := []struct {
		name   string
		status int
		expiry int64
		quota  int
		want   int
	}{
		{name: "enabled", status: common.TokenStatusEnabled, expiry: -1, quota: 100, want: http.StatusNoContent},
		{name: "disabled", status: common.TokenStatusDisabled, expiry: -1, quota: 100, want: http.StatusUnauthorized},
		{name: "expired", status: common.TokenStatusEnabled, expiry: 1, quota: 100, want: http.StatusNoContent},
		{name: "exhausted", status: common.TokenStatusExhausted, expiry: -1, quota: 0, want: http.StatusNoContent},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := tt.name + "key"
			token := model.Token{UserId: user.Id, Key: key, Name: tt.name, Status: tt.status, ExpiredTime: tt.expiry, RemainQuota: tt.quota}
			require.NoError(t, db.Create(&token).Error)

			r := gin.New()
			r.GET("/readonly", TokenAuthReadOnly(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
			req := httptest.NewRequest(http.MethodGet, "/readonly", nil)
			req.Header.Set("Authorization", "Bearer sk-"+key)
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)
			assert.Equal(t, tt.want, resp.Code, "case %d", i)
		})
	}
}
