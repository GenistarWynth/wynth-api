package model

import (
	"database/sql"
	"errors"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizedEmailLockName(t *testing.T) {
	first := normalizedEmailLockName(" User@Example.COM ")
	assert.LessOrEqual(t, len(first), 64)
	assert.Equal(t, first, normalizedEmailLockName("user@example.com"))
	assert.NotEqual(t, first, normalizedEmailLockName("other@example.com"))
	assert.Contains(t, first, "new-api:email:")
}

func TestCompleteLockedTransactionReleasesAfterCompletion(t *testing.T) {
	transactionErr := errors.New("transaction failed")
	releaseErr := errors.New("release failed")
	tests := []struct {
		name            string
		transactionErr  error
		releaseErr      error
		wantTransaction bool
		wantRelease     bool
	}{
		{name: "commit then release"},
		{name: "rollback then release", transactionErr: transactionErr, wantTransaction: true},
		{name: "release failure after commit", releaseErr: releaseErr, wantRelease: true},
		{name: "transaction error stays primary and release error is retained", transactionErr: transactionErr, releaseErr: releaseErr, wantTransaction: true, wantRelease: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := make([]string, 0, 2)
			err := completeLockedTransaction(func() error {
				events = append(events, "transaction complete")
				return tt.transactionErr
			}, func() error {
				events = append(events, "release")
				return tt.releaseErr
			})
			assert.Equal(t, []string{"transaction complete", "release"}, events)
			assert.Equal(t, tt.wantTransaction, errors.Is(err, transactionErr))
			assert.Equal(t, tt.wantRelease, errors.Is(err, releaseErr))
		})
	}
}

func TestCompleteLockedTransactionReleasesAfterPanic(t *testing.T) {
	events := make([]string, 0, 2)
	panicValue := &struct{ message string }{message: "transaction panic"}

	var recovered any
	func() {
		defer func() {
			recovered = recover()
		}()
		_ = completeLockedTransaction(func() error {
			events = append(events, "completion")
			panic(panicValue)
		}, func() error {
			events = append(events, "release")
			return nil
		})
	}()

	assert.Equal(t, []string{"completion", "release"}, events)
	assert.Same(t, panicValue, recovered)
}

func TestCompleteLockedTransactionObservesReleaseFailureAfterPanic(t *testing.T) {
	panicValue := &struct{ message string }{message: "transaction panic"}
	releaseErr := errors.New("release failed")
	oldReport := reportLockedTransactionReleaseError
	var reported string
	reportLockedTransactionReleaseError = func(message string) { reported = message }
	t.Cleanup(func() { reportLockedTransactionReleaseError = oldReport })

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = completeLockedTransaction(
			func() error { panic(panicValue) },
			func() error { return releaseErr },
		)
	}()

	assert.ErrorContains(t, errors.New(reported), "release normalized email lock")
	assert.ErrorContains(t, errors.New(reported), releaseErr.Error())
	assert.Same(t, panicValue, recovered)
}

func TestClassifyMySQLGetLockResult(t *testing.T) {
	tests := []struct {
		name    string
		result  sql.NullInt64
		wantErr string
	}{
		{name: "NULL", result: sql.NullInt64{}, wantErr: "GET_LOCK returned NULL"},
		{name: "timeout", result: sql.NullInt64{Int64: 0, Valid: true}, wantErr: "timed out acquiring normalized email lock"},
		{name: "success", result: sql.NullInt64{Int64: 1, Valid: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := classifyMySQLGetLockResult(tt.result)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tt.wantErr)
		})
	}
}

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
