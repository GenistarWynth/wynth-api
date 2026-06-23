package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

// These service-local Gin context keys are prefixed with account_pool_ to avoid
// collisions with the shared constant.ContextKey* namespace.
const (
	accountPoolAttemptedAccountIDsContextKey = "account_pool_attempted_account_ids"
	accountPoolSelectedPoolIDContextKey      = "account_pool_selected_pool_id"
	accountPoolSelectedBindingIDContextKey   = "account_pool_selected_binding_id"
	accountPoolSelectedAccountIDContextKey   = "account_pool_selected_account_id"
	accountPoolSelectedRetryTimesContextKey  = "account_pool_selected_retry_times"
)

func ApplyAccountPoolRuntimeSelection(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request) error {
	clearSelectedAccountPoolRuntimeSelection(c)
	if c == nil || info == nil || info.ChannelMeta == nil {
		return nil
	}
	selection, release, err := SelectAccountPoolAccountWithLease(AccountPoolSelectionRequest{
		ChannelID:            info.ChannelId,
		RequestModel:         info.OriginModelName,
		ChannelUpstreamModel: info.UpstreamModelName,
		AttemptedAccountIDs:  GetAccountPoolAttemptedAccountIDs(c),
	})
	if err != nil {
		if errors.Is(err, ErrAccountPoolBindingNotRuntimeEnabled) {
			return nil
		}
		// Phase 2C must map ErrAccountPoolNoSchedulableAccount to a retriable 503.
		return err
	}
	releaseStored := false
	defer func() {
		if !releaseStored {
			release()
		}
	}()

	runtimeCredential := strings.TrimSpace(selection.Credential.APIKey)
	if runtimeCredential == "" {
		runtimeCredential = strings.TrimSpace(selection.TokenState.AccessToken)
	}
	if runtimeCredential == "" {
		return errors.New("account pool selected account has no runtime credential")
	}

	info.ApiKey = runtimeCredential
	info.UpstreamModelName = selection.UpstreamModelName
	if selection.ProxyURL != "" {
		info.RuntimeProxy = selection.ProxyURL
	}
	if request != nil {
		request.SetModelName(selection.UpstreamModelName)
	}
	c.Set(accountPoolSelectedPoolIDContextKey, selection.PoolID)
	c.Set(accountPoolSelectedBindingIDContextKey, selection.BindingID)
	c.Set(accountPoolSelectedAccountIDContextKey, selection.AccountID)
	c.Set(accountPoolSelectedRetryTimesContextKey, selection.AccountRetryTimes)
	AddAccountPoolAttemptedAccountID(c, selection.AccountID)
	setAccountPoolRuntimeLeaseRelease(c, release)
	releaseStored = true
	return nil
}

func GetAccountPoolAttemptedAccountIDs(c *gin.Context) map[int]struct{} {
	if c == nil {
		return map[int]struct{}{}
	}
	if attempted, ok := c.Get(accountPoolAttemptedAccountIDsContextKey); ok {
		if accountIDs, ok := attempted.(map[int]struct{}); ok && accountIDs != nil {
			return accountIDs
		}
	}
	return map[int]struct{}{}
}

func AddAccountPoolAttemptedAccountID(c *gin.Context, accountID int) {
	if c == nil || accountID <= 0 {
		return
	}
	accountIDs := GetAccountPoolAttemptedAccountIDs(c)
	accountIDs[accountID] = struct{}{}
	c.Set(accountPoolAttemptedAccountIDsContextKey, accountIDs)
}

func GetSelectedAccountPoolAccountID(c *gin.Context) int {
	if c == nil {
		return 0
	}
	return c.GetInt(accountPoolSelectedAccountIDContextKey)
}

func GetSelectedAccountPoolAccountRetryTimes(c *gin.Context) int {
	if c == nil {
		return 0
	}
	return c.GetInt(accountPoolSelectedRetryTimesContextKey)
}

func clearSelectedAccountPoolRuntimeSelection(c *gin.Context) {
	if c == nil {
		return
	}
	c.Set(accountPoolSelectedPoolIDContextKey, 0)
	c.Set(accountPoolSelectedBindingIDContextKey, 0)
	c.Set(accountPoolSelectedAccountIDContextKey, 0)
	c.Set(accountPoolSelectedRetryTimesContextKey, 0)
}
