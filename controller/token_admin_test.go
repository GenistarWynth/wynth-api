package controller

import (
	"net/http/httptest"
	"testing"

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
