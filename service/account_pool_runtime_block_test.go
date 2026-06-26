package service

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Block manager unit tests ---

func TestAccountPoolRuntimeBlockManagerBlockAndIsBlocked(t *testing.T) {
	resetAccountPoolRuntimeBlocksForTest()

	now := int64(1000)
	blockAccountPoolRuntime(42, now+300)

	assert.True(t, accountPoolRuntimeBlocked(42, now), "account should be blocked before blockedUntil")
	assert.True(t, accountPoolRuntimeBlocked(42, now+299), "account should still be blocked one second before expiry")
	assert.False(t, accountPoolRuntimeBlocked(42, now+300), "account should not be blocked at exactly blockedUntil")
	assert.False(t, accountPoolRuntimeBlocked(42, now+301), "account should not be blocked after blockedUntil")
}

func TestAccountPoolRuntimeBlockManagerClear(t *testing.T) {
	resetAccountPoolRuntimeBlocksForTest()

	now := int64(1000)
	blockAccountPoolRuntime(42, now+300)
	require.True(t, accountPoolRuntimeBlocked(42, now))

	clearAccountPoolRuntimeBlock(42)

	assert.False(t, accountPoolRuntimeBlocked(42, now), "account should be unblocked after clear")
}

func TestAccountPoolRuntimeBlockManagerMonotonicMax(t *testing.T) {
	resetAccountPoolRuntimeBlocksForTest()

	now := int64(1000)
	blockAccountPoolRuntime(42, now+600)
	// Setting a shorter duration must NOT shorten the existing block.
	blockAccountPoolRuntime(42, now+100)

	assert.True(t, accountPoolRuntimeBlocked(42, now+500), "shorter block must not overwrite longer one")
	assert.False(t, accountPoolRuntimeBlocked(42, now+600), "expired after original longer block")
}

func TestAccountPoolRuntimeBlockManagerMonotonicMaxExtends(t *testing.T) {
	resetAccountPoolRuntimeBlocksForTest()

	now := int64(1000)
	blockAccountPoolRuntime(42, now+100)
	// Setting a longer duration must extend the block.
	blockAccountPoolRuntime(42, now+500)

	assert.True(t, accountPoolRuntimeBlocked(42, now+400), "longer block must extend the existing one")
	assert.False(t, accountPoolRuntimeBlocked(42, now+500), "expired after new longer block")
}

func TestAccountPoolRuntimeBlockManagerIgnoresInvalidInputs(t *testing.T) {
	resetAccountPoolRuntimeBlocksForTest()

	now := int64(1000)
	// accountID <= 0 and until <= 0 must both be ignored.
	blockAccountPoolRuntime(0, now+300)
	blockAccountPoolRuntime(-1, now+300)
	blockAccountPoolRuntime(42, 0)
	blockAccountPoolRuntime(42, -1)

	assert.False(t, accountPoolRuntimeBlocked(0, now))
	assert.False(t, accountPoolRuntimeBlocked(-1, now))
	assert.False(t, accountPoolRuntimeBlocked(42, now))
}

func TestAccountPoolRuntimeBlockManagerLazilyDeletesExpiredEntries(t *testing.T) {
	resetAccountPoolRuntimeBlocksForTest()

	now := int64(1000)
	blockAccountPoolRuntime(99, now+10)
	// Query past expiry — the entry should be lazily removed and return false.
	assert.False(t, accountPoolRuntimeBlocked(99, now+10), "not blocked at expiry")
	// Re-check: the entry was lazily deleted; calling again must still return false.
	assert.False(t, accountPoolRuntimeBlocked(99, now+100), "not blocked well after expiry")
}

func TestAccountPoolRuntimeBlockManagerResetIsolatesState(t *testing.T) {
	resetAccountPoolRuntimeBlocksForTest()
	blockAccountPoolRuntime(77, int64(9999999))
	require.True(t, accountPoolRuntimeBlocked(77, int64(1000)))

	resetAccountPoolRuntimeBlocksForTest()

	assert.False(t, accountPoolRuntimeBlocked(77, int64(1000)), "reset must clear all blocks")
}

// --- Scheduler integration: in-process block excludes account even when DB shows it schedulable ---

func TestAccountPoolSchedulerExcludesRuntimeBlockedAccount(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, 2) // ChannelStatusManuallyDisabled = 2
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	accountA := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "account-a"})
	accountB := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "account-b"})

	now := int64(500)
	// Block account A in-process; DB still shows it schedulable (no cooldown set).
	blockAccountPoolRuntime(accountA.Id, now+300)

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  now,
	})

	require.NoError(t, err)
	assert.Equal(t, accountB.Id, result.AccountID, "runtime-blocked account A must be excluded; account B must be selected")
}

func TestAccountPoolSchedulerExcludesRuntimeBlockedAccountUntilExpiry(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, 2)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	accountA := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "account-a", Priority: 100})
	accountB := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "account-b", Priority: 10})

	now := int64(500)
	blockUntil := now + 300
	blockAccountPoolRuntime(accountA.Id, blockUntil)

	// Before expiry: A is excluded, B is selected.
	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  now,
	})
	require.NoError(t, err)
	assert.Equal(t, accountB.Id, result.AccountID, "should fall back to B while A is blocked")

	// After expiry: A is eligible again (higher priority, selected by round-robin or priority).
	result, err = SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  blockUntil + 1,
	})
	require.NoError(t, err)
	assert.Equal(t, accountA.Id, result.AccountID, "A should be eligible again after block expiry")
}

func TestAccountPoolSchedulerEligibleAfterRuntimeBlockCleared(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	channel := createAccountPoolServiceTestChannel(t, 2)
	createEnabledAccountPoolSchedulerBinding(t, pool.Id, channel.Id, AccountPoolAccountFilterConfig{}, AccountPoolModelPolicy{})

	accountA := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "account-a", Priority: 100})
	_ = createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "account-b", Priority: 10})

	now := int64(500)
	blockAccountPoolRuntime(accountA.Id, now+300)
	require.False(t, accountPoolRuntimeBlocked(accountA.Id, now+300)) // sanity: at expiry it's already unblocked

	// Block and immediately clear.
	blockAccountPoolRuntime(accountA.Id, now+300)
	clearAccountPoolRuntimeBlock(accountA.Id)

	result, err := SelectAccountPoolAccount(AccountPoolSelectionRequest{
		ChannelID:            channel.Id,
		RequestModel:         "gpt-5",
		ChannelUpstreamModel: "gpt-5",
		Now:                  now,
	})
	require.NoError(t, err)
	assert.Equal(t, accountA.Id, result.AccountID, "A should be selected after block is cleared")
}

// --- Failure sets the in-process block ---

func TestRecordAccountPoolRuntimeAttemptFailureSetsRuntimeBlock429(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "rate-limited"})

	now := int64(1000)
	err := types.NewErrorWithStatusCode(errors.New("too many requests"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, now))

	assert.True(t, accountPoolRuntimeBlocked(account.Id, now), "account must be runtime-blocked immediately after 429 failure")
}

func TestRecordAccountPoolRuntimeAttemptFailureSetsRuntimeBlock5xx(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "server-error"})

	now := int64(1000)
	err := types.NewErrorWithStatusCode(errors.New("bad gateway"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway)
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, now))

	assert.True(t, accountPoolRuntimeBlocked(account.Id, now), "account must be runtime-blocked immediately after 5xx failure")
}

func TestRecordAccountPoolRuntimeAttemptFailureRuntimeBlockCappedAt600s(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "rate-limited-capped"})

	now := int64(1000)
	// Seed a very long existing rate_limited_until to exercise the cap.
	require.NoError(t, model.DB.Model(&model.AccountPoolAccount{}).
		Where("id = ?", account.Id).
		Update("rate_limited_until", now+9999).Error)

	err := types.NewErrorWithStatusCode(errors.New("too many requests"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests)
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, now))

	// Block must be set, and must not be blocked past now+accountPoolRuntimeBlockCapSeconds.
	assert.True(t, accountPoolRuntimeBlocked(account.Id, now), "account must be blocked")
	assert.False(t, accountPoolRuntimeBlocked(account.Id, now+accountPoolRuntimeBlockCapSeconds), "block must be capped at now+cap")
}

func TestRecordAccountPoolRuntimeAttemptFailureSetsFloorBlockWhenNoSpecificCooldown(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	// A 401 on a non-OAuth account → expires immediately, no cooldown timestamp.
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "expired-account"})

	now := int64(1000)
	err := types.NewErrorWithStatusCode(errors.New("unauthorized"), types.ErrorCodeBadResponseStatusCode, http.StatusUnauthorized)
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, now))

	// Account expired: floor block (accountPoolRuntimeBlockFloorSeconds) must still be set.
	assert.True(t, accountPoolRuntimeBlocked(account.Id, now), "expired account must still get a runtime block")
	// Must be blocked for at least floor seconds.
	assert.True(t, accountPoolRuntimeBlocked(account.Id, now+accountPoolRuntimeBlockFloorSeconds-1), "must be blocked for floor duration")
	// Must not be blocked past cap.
	assert.False(t, accountPoolRuntimeBlocked(account.Id, now+accountPoolRuntimeBlockCapSeconds), "block must not exceed cap")
}

// --- FIX 2: No block for no-cooldown error codes ---

func TestRecordAccountPoolRuntimeAttemptFailureNoBlockFor404(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "not-found"})

	now := int64(1000)
	err := types.NewErrorWithStatusCode(errors.New("not found"), types.ErrorCodeBadResponseStatusCode, http.StatusNotFound)
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, now))

	// 404 is a no-cooldown code: DB keeps account schedulable, no in-process block must be set.
	assert.False(t, accountPoolRuntimeBlocked(account.Id, now), "404 must NOT set a runtime block")
}

func TestRecordAccountPoolRuntimeAttemptFailureBlocksFor500(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "server-error-block"})

	now := int64(1000)
	err := types.NewErrorWithStatusCode(errors.New("internal server error"), types.ErrorCodeBadResponseStatusCode, http.StatusInternalServerError)
	require.NoError(t, RecordAccountPoolRuntimeAttemptFailure(account.Id, err, now))

	// 500 sets temp_disabled_until → must set an in-process block (regression guard).
	assert.True(t, accountPoolRuntimeBlocked(account.Id, now), "500 must set a runtime block")
}

// --- Success clears the in-process block ---

func TestRecordAccountPoolRuntimeAttemptSuccessClearsRuntimeBlock(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	resetAccountPoolRuntimeBlocksForTest()

	svc := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, svc)
	account := createAccountPoolSchedulerAccount(t, svc, pool.Id, AccountPoolAccountCreateParams{Name: "recovered"})

	now := int64(1000)
	blockAccountPoolRuntime(account.Id, now+300)
	require.True(t, accountPoolRuntimeBlocked(account.Id, now))

	require.NoError(t, RecordAccountPoolRuntimeAttemptSuccess(account.Id, now))

	assert.False(t, accountPoolRuntimeBlocked(account.Id, now), "success must clear the in-process block")
}
