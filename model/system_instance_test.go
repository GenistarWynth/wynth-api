package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func resetSystemInstancesForTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&SystemInstance{}))
	require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SystemInstance{}).Error)
	t.Cleanup(func() {
		require.NoError(t, DB.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&SystemInstance{}).Error)
	})
}

func TestDeleteStaleSystemInstancesUsesStrictBoundaryAndExactCount(t *testing.T) {
	resetSystemInstancesForTest(t)
	const now int64 = 1000
	instances := []SystemInstance{
		{NodeName: "stale", LastSeenAt: now - SystemInstanceStaleAfterSeconds - 1},
		{NodeName: "boundary", LastSeenAt: now - SystemInstanceStaleAfterSeconds},
		{NodeName: "online", LastSeenAt: now - SystemInstanceStaleAfterSeconds + 1},
	}
	require.NoError(t, DB.Create(&instances).Error)

	deleted, err := DeleteStaleSystemInstances(now)

	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)
	var remaining []SystemInstance
	require.NoError(t, DB.Order("node_name asc").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	assert.Equal(t, "boundary", remaining[0].NodeName)
	assert.Equal(t, "online", remaining[1].NodeName)
}

func TestDeleteStaleSystemInstanceConditionallyDeletesCurrentRow(t *testing.T) {
	resetSystemInstancesForTest(t)
	const now int64 = 2000
	require.NoError(t, DB.Create(&SystemInstance{NodeName: "refreshed", LastSeenAt: now - SystemInstanceStaleAfterSeconds - 1}).Error)
	require.NoError(t, DB.Model(&SystemInstance{}).Where("node_name = ?", "refreshed").Update("last_seen_at", now-1).Error)

	deleted, err := DeleteStaleSystemInstance("refreshed", now)

	require.NoError(t, err)
	assert.False(t, deleted)
	var kept SystemInstance
	require.NoError(t, DB.First(&kept, "node_name = ?", "refreshed").Error)
	assert.Equal(t, now-1, kept.LastSeenAt)

	require.NoError(t, DB.Create(&SystemInstance{NodeName: "stale", LastSeenAt: now - SystemInstanceStaleAfterSeconds - 1}).Error)
	deleted, err = DeleteStaleSystemInstance("stale", now)
	require.NoError(t, err)
	assert.True(t, deleted)
	assert.ErrorIs(t, DB.First(&SystemInstance{}, "node_name = ?", "stale").Error, gorm.ErrRecordNotFound)
}
