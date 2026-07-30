package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDisableChannelSanitizesStoredAndNotificationReasonAtBoundary(t *testing.T) {
	previousDB := model.DB
	previousMemoryCache := common.MemoryCacheEnabled
	db, err := gorm.Open(sqlite.Open("file:disable-channel-sanitizer?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}, &model.User{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	previousNotifyRootUser := notifyRootUser
	var notificationContent string
	notifyRootUser = func(_ string, _ string, content string) {
		notificationContent = content
	}
	t.Cleanup(func() {
		model.DB = previousDB
		common.MemoryCacheEnabled = previousMemoryCache
		notifyRootUser = previousNotifyRootUser
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})

	channel := &model.Channel{
		Id:      701,
		Name:    "sanitizer-channel",
		Status:  common.ChannelStatusEnabled,
		AutoBan: common.GetPointer(1),
	}
	require.NoError(t, db.Create(channel).Error)
	require.NoError(t, db.Create(&model.User{
		Id:       702,
		Username: "root",
		Role:     common.RoleRootUser,
		Status:   common.UserStatusEnabled,
	}).Error)

	const safe = "provider capacity exhausted"
	const secret = "disable-cookie-secret"
	reason := safe + ` {"Set-Cookie":"session=` + secret + `; Path=/"}`
	DisableChannel(*types.NewChannelError(channel.Id, channel.Type, channel.Name, false, "", true), reason)

	var stored model.Channel
	require.NoError(t, db.First(&stored, channel.Id).Error)
	statusReason := common.Interface2String(stored.GetOtherInfo()["status_reason"])
	assert.Contains(t, statusReason, safe)
	assert.NotContains(t, statusReason, secret)
	assert.Contains(t, notificationContent, safe)
	assert.NotContains(t, notificationContent, secret)
}
