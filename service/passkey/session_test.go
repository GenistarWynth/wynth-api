package passkey

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/glebarez/sqlite"
	webauthn "github.com/go-webauthn/webauthn/webauthn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPasskeyStepUpFlowCarriesExactResourceAndIsConsumedOnce(t *testing.T) {
	previousDB := model.DB
	previousSecret := common.SessionSecret
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	model.DB = db
	common.SessionSecret = "passkey-resource-flow-test-secret"
	t.Cleanup(func() {
		model.DB = previousDB
		common.SessionSecret = previousSecret
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})

	token, _, err := CreateSessionDataFlow(
		model.AuthFlowPurposePasskeyStepUp,
		42,
		"session-a",
		"account_pool.credentials.export",
		"17",
		&webauthn.SessionData{},
	)
	require.NoError(t, err)

	_, scope, resource, err := PopSessionDataFlow(
		token,
		model.AuthFlowPurposePasskeyStepUp,
		42,
		"session-a",
	)
	require.NoError(t, err)
	assert.Equal(t, "account_pool.credentials.export", scope)
	assert.Equal(t, "17", resource)

	_, _, _, err = PopSessionDataFlow(
		token,
		model.AuthFlowPurposePasskeyStepUp,
		42,
		"session-a",
	)
	assert.Error(t, err)
}
