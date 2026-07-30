package model

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relaykit/dto"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupUserUpdateTestState(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM users").Error)

	oldRedisEnabled := common.RedisEnabled
	oldBatchUpdateEnabled := common.BatchUpdateEnabled
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	t.Cleanup(func() {
		common.RedisEnabled = oldRedisEnabled
		common.BatchUpdateEnabled = oldBatchUpdateEnabled
	})
}

func setupUserCacheRedisTest(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	oldRDB := common.RDB
	oldRedisEnabled := common.RedisEnabled
	oldSyncFrequency := common.SyncFrequency
	common.RDB = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	common.RedisEnabled = true
	common.SyncFrequency = 60
	t.Cleanup(func() {
		require.NoError(t, common.RDB.Close())
		common.RDB = oldRDB
		common.RedisEnabled = oldRedisEnabled
		common.SyncFrequency = oldSyncFrequency
		mr.Close()
	})

	return mr
}

func TestUserUpdateDoesNotOverwriteAccountingFields(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           1,
		Username:     "quota-race-user",
		Password:     "password",
		DisplayName:  "before",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	staleUser, err := GetUserById(user.Id, true)
	require.NoError(t, err)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 400),
		"used_quota":    gorm.Expr("used_quota + ?", 400),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	staleUser.DisplayName = "after"
	require.NoError(t, staleUser.Update(false))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, "after", got.DisplayName)
	assert.Equal(t, 600, got.Quota)
	assert.Equal(t, 420, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
}

func TestUpdateUserSettingOnlyUpdatesSetting(t *testing.T) {
	setupUserUpdateTestState(t)

	user := User{
		Id:           2,
		Username:     "setting-user",
		Password:     "password",
		Status:       common.UserStatusEnabled,
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 3,
	}
	require.NoError(t, DB.Create(&user).Error)

	require.NoError(t, DB.Model(&User{}).Where("id = ?", user.Id).Updates(map[string]interface{}{
		"quota":         gorm.Expr("quota - ?", 250),
		"used_quota":    gorm.Expr("used_quota + ?", 250),
		"request_count": gorm.Expr("request_count + ?", 1),
	}).Error)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh"}))

	var got User
	require.NoError(t, DB.First(&got, user.Id).Error)
	assert.Equal(t, 750, got.Quota)
	assert.Equal(t, 270, got.UsedQuota)
	assert.Equal(t, 4, got.RequestCount)
	assert.Equal(t, "zh", got.GetSetting().Language)
}

func TestUserUpdatePreservesAtomicQuotaInRedisCache(t *testing.T) {
	setupUserUpdateTestState(t)
	mr := setupUserCacheRedisTest(t)

	user := User{
		Username: "cached-user",
		Password: "password-hash",
		Email:    "before@example.com",
		Group:    "default",
		Quota:    100,
		Status:   common.UserStatusEnabled,
	}
	user.SetSetting(dto.UserSetting{Language: "en"})
	require.NoError(t, DB.Create(&user).Error)
	require.NoError(t, populateUserCache(user))
	assert.Greater(t, mr.TTL(getUserCacheKey(user.Id)), time.Duration(0))

	require.NoError(t, cacheDecrUserQuota(user.Id, 30))

	stale := user
	stale.Username = "updated-cached-user"
	stale.Email = "after@example.com"
	stale.Group = "vip"
	stale.Status = common.UserStatusDisabled
	stale.SetSetting(dto.UserSetting{Language: "zh"})
	require.NoError(t, stale.Update(false))

	cached, err := cacheGetUserBase(user.Id)
	require.NoError(t, err)
	assert.Equal(t, 70, cached.Quota)
	assert.Equal(t, "updated-cached-user", cached.Username)
	assert.Equal(t, "after@example.com", cached.Email)
	assert.Equal(t, "vip", cached.Group)
	assert.Equal(t, common.UserStatusDisabled, cached.Status)
	assert.Equal(t, stale.Setting, cached.Setting)
}

func TestUpdateUserSettingInvalidatesCacheWhenFieldRefreshFails(t *testing.T) {
	setupUserUpdateTestState(t)
	mr := setupUserCacheRedisTest(t)

	user := User{Username: "cache-fallback", Password: "password-hash", Status: common.UserStatusEnabled}
	user.SetSetting(dto.UserSetting{Language: "en"})
	require.NoError(t, DB.Create(&user).Error)

	cacheKey := getUserCacheKey(user.Id)
	mr.Set(cacheKey, "wrong-type")
	mr.SetTTL(cacheKey, time.Minute)

	require.NoError(t, UpdateUserSetting(user.Id, dto.UserSetting{Language: "zh"}))
	assert.False(t, mr.Exists(cacheKey))

	var stored User
	require.NoError(t, DB.First(&stored, user.Id).Error)
	assert.Equal(t, "zh", stored.GetSetting().Language)
}

func TestValidateNormalizedEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   string
		want    string
		wantErr bool
	}{
		{name: "normalizes", email: " User@Example.COM ", want: "user@example.com"},
		{name: "rejects malformed", email: "not-an-email", wantErr: true},
		{name: "rejects over max length", email: "abcdefghijklmnopqrstuvwxyz12345678901234567890@example.com", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateNormalizedEmail(tt.email)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateAndFillMatchesNormalizedEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)
	hash, err := common.Password2Hash("Password123")
	require.NoError(t, err)
	require.NoError(t, DB.Create(&User{Username: "email-login", Password: hash, Email: "user@example.com", Status: common.UserStatusEnabled}).Error)

	login := User{Username: " USER@EXAMPLE.COM ", Password: "Password123"}
	require.NoError(t, login.ValidateAndFill())
	assert.Equal(t, "email-login", login.Username)
}

func TestValidateAndFillRejectsAmbiguousLegacyEmailButKeepsExactUsername(t *testing.T) {
	setupUserUpdateTestState(t)
	hash, err := common.Password2Hash("Password123")
	require.NoError(t, err)
	for _, user := range []User{
		{Username: "legacy-a", Password: hash, Email: "legacy@example.com", AffCode: "legacy-a", Status: common.UserStatusEnabled},
		{Username: "legacy-b", Password: hash, Email: "LEGACY@example.com", AffCode: "legacy-b", Status: common.UserStatusEnabled},
	} {
		require.NoError(t, DB.Create(&user).Error)
	}

	emailLogin := User{Username: " Legacy@Example.COM ", Password: "Password123"}
	require.ErrorIs(t, emailLogin.ValidateAndFill(), ErrInvalidCredentials)

	usernameLogin := User{Username: "legacy-b", Password: "Password123"}
	require.NoError(t, usernameLogin.ValidateAndFill())
	assert.Equal(t, "legacy-b", usernameLogin.Username)
}

func TestEnsureEmailAvailableRejectsExistingEmailCaseInsensitive(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "Taken@Example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := EnsureEmailAvailable(" taken@example.COM ", 0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	user, err := GetUniqueUserByEmail("TAKEN@example.com")
	require.NoError(t, err)
	assert.Equal(t, "existing", user.Username)

	require.NoError(t, EnsureEmailAvailable("taken@example.com", user.Id))
}

func TestInsertRejectsDuplicateEmailWithoutUniqueIndex(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "existing",
		Password: "old-password",
		Email:    "taken@example.com",
		Status:   common.UserStatusEnabled,
	}).Error)

	user := &User{
		Username: "oauth-user",
		Email:    "TAKEN@example.com",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	err := user.Insert(0)
	require.ErrorIs(t, err, ErrEmailAlreadyTaken)

	var count int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", "oauth-user").Count(&count).Error)
	assert.Zero(t, count)
}

func TestConcurrentInsertDoesNotPersistDuplicateNormalizedEmail(t *testing.T) {
	setupUserUpdateTestState(t)

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, email := range []string{"race@example.com", " RACE@EXAMPLE.COM "} {
		wg.Add(1)
		go func(i int, email string) {
			defer wg.Done()
			<-start
			user := &User{Username: "race-user-" + string(rune('a'+i)), Email: email, Status: common.UserStatusEnabled}
			errs <- user.Insert(0)
		}(i, email)
	}
	close(start)
	wg.Wait()
	close(errs)

	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	require.Equal(t, 1, successes)
	var count int64
	require.NoError(t, DB.Model(&User{}).Where("LOWER(email) = ?", "race@example.com").Count(&count).Error)
	require.Equal(t, int64(1), count)
}

func TestInsertKeepsBlankPasswordForPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	user := &User{
		Username: "passwordless-user",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}

	require.NoError(t, user.Insert(0))

	var stored User
	require.NoError(t, DB.Where("username = ?", user.Username).First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestValidateAndFillRejectsPasswordlessUser(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "passwordless-user",
		Password: "",
		Status:   common.UserStatusEnabled,
	}).Error)

	loginUser := User{
		Username: "passwordless-user",
		Password: "NewPassword123",
	}
	err := loginUser.ValidateAndFill()
	require.ErrorIs(t, err, ErrInvalidCredentials)

	var stored User
	require.NoError(t, DB.Where("username = ?", "passwordless-user").First(&stored).Error)
	assert.Empty(t, stored.Password)
}

func TestResetUserPasswordByEmailRequiresSingleActiveMatch(t *testing.T) {
	setupUserUpdateTestState(t)

	require.NoError(t, DB.Create(&User{
		Username: "duplicate-1",
		Password: "old-1",
		Email:    "legacy@example.com",
		AffCode:  "dupe1",
		Status:   common.UserStatusEnabled,
	}).Error)
	require.NoError(t, DB.Create(&User{
		Username: "duplicate-2",
		Password: "old-2",
		Email:    "LEGACY@example.com",
		AffCode:  "dupe2",
		Status:   common.UserStatusEnabled,
	}).Error)

	err := ResetUserPasswordByEmail("legacy@example.com", "NewPassword123")
	require.ErrorIs(t, err, ErrEmailAmbiguous)

	var duplicates []User
	require.NoError(t, DB.Where("LOWER(email) = ?", "legacy@example.com").Order("username asc").Find(&duplicates).Error)
	require.Len(t, duplicates, 2)
	assert.Equal(t, "old-1", duplicates[0].Password)
	assert.Equal(t, "old-2", duplicates[1].Password)

	require.NoError(t, DB.Create(&User{
		Username: "unique",
		Password: "old",
		Email:    "unique@example.com",
		AffCode:  "unique",
		Status:   common.UserStatusEnabled,
	}).Error)

	require.NoError(t, ResetUserPasswordByEmail("UNIQUE@example.com", "NewPassword123"))

	var unique User
	require.NoError(t, DB.Where("username = ?", "unique").First(&unique).Error)
	assert.True(t, common.ValidatePasswordAndHash("NewPassword123", unique.Password))

	err = ResetUserPasswordByEmail("missing@example.com", "NewPassword123")
	require.True(t, errors.Is(err, ErrEmailNotFound))
}
