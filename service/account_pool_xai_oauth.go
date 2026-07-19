package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	maxAccountPoolXAISSOImportAccounts = 25

	accountPoolXAISSOImportConcurrencyEnv     = "ACCOUNT_POOL_XAI_SSO_IMPORT_CONCURRENCY"
	accountPoolXAISSOImportTimeoutEnv         = "ACCOUNT_POOL_XAI_SSO_IMPORT_TIMEOUT_SECONDS"
	accountPoolXAISSOImportDefaultConcurrency = 3
	accountPoolXAISSOImportMaxConcurrency     = 8
	accountPoolXAISSOImportDefaultTimeout     = 5 * time.Minute
	accountPoolXAISSOImportMinTimeout         = 30 * time.Second
	accountPoolXAISSOImportMaxTimeout         = 30 * time.Minute
)

type accountPoolXAIOAuthInfoRefreshFunc func(context.Context, string, string, string) (*XAIOAuthTokenInfo, error)
type accountPoolXAISSOConvertFunc func(context.Context, string, string) (*XAIOAuthTokenInfo, error)

var (
	accountPoolXAIOAuthInfoRefresh accountPoolXAIOAuthInfoRefreshFunc = RefreshXAIOAuthTokenInfoWithProxy
	accountPoolXAISSOConvert       accountPoolXAISSOConvertFunc       = ConvertXAISSOToOAuth
)

type AccountPoolXAISSOImportParams struct {
	PoolID            int
	SSOTokens         []string
	Name              string
	Status            string
	Priority          int64
	Weight            uint
	MaxConcurrency    int
	MaxConcurrencySet bool
	ProxyID           int
	SupportedModels   []string
	ModelMapping      map[string]string
}

type AccountPoolXAISSOImportError struct {
	Index   int    `json:"index"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

type AccountPoolXAISSOImportResult struct {
	Created []AccountPoolAccountView       `json:"created"`
	Errors  []AccountPoolXAISSOImportError `json:"errors"`
}

type accountPoolXAISSOConversionResult struct {
	Info      *XAIOAuthTokenInfo
	Err       error
	Attempted bool
}

func (s AccountPoolService) RefreshXAIOAuthAccount(ctx context.Context, poolID int, accountID int) (AccountPoolAccountView, error) {
	pool, err := getAccountPoolExistingPool(poolID)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	if pool.Platform != model.AccountPoolPlatformXAI {
		return AccountPoolAccountView{}, errors.New("account pool is not an xai pool")
	}
	account, err := getAccountPoolAccountForPool(poolID, accountID)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	view, _, err := refreshXAIOAuthAccountSnapshot(ctx, pool, account, common.GetTimestamp())
	return view, err
}

func refreshXAIOAuthAccountSnapshot(ctx context.Context, pool model.AccountPool, account model.AccountPoolAccount, now int64) (AccountPoolAccountView, bool, error) {
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	if err != nil {
		return AccountPoolAccountView{}, false, err
	}
	if !strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeOAuth) {
		return AccountPoolAccountView{}, false, errors.New("account is not an xai oauth account")
	}
	tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
	if err != nil {
		return AccountPoolAccountView{}, false, err
	}
	refreshToken := accountPoolRuntimeRefreshToken(credential, tokenState)
	if refreshToken == "" {
		return AccountPoolAccountView{}, false, errors.New("xai oauth refresh_token is required")
	}
	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(account.ProxyID, pool.DefaultProxyID)
	if err != nil {
		return AccountPoolAccountView{}, false, err
	}
	info, err := accountPoolXAIOAuthInfoRefresh(ctx, refreshToken, proxyURL, credential.ClientID)
	if err != nil {
		if IsXAIOAuthPermanentCredentialError(err) {
			rows, _ := expireAccountPoolXAIOAuthCredentialFailure(account.Id, account.CredentialConfig, account.TokenState, err, now)
			return AccountPoolAccountView{}, rows > 0, err
		}
		_, _ = markAccountPoolXAIOAuthTransientFailure(account, err, now)
		return AccountPoolAccountView{}, false, err
	}
	updatedCredential := mergeAccountPoolCredentialUpdate(credential, info.AccountPoolCredential())
	updatedTokenState := info.AccountPoolTokenState()
	updatedTokenState.Version = tokenState.Version + 1
	if updatedTokenState.Version <= 0 {
		updatedTokenState.Version = 1
	}
	encryptedCredential, err := EncryptAccountPoolCredentialConfig(updatedCredential)
	if err != nil {
		return AccountPoolAccountView{}, false, err
	}
	encryptedTokenState, err := EncryptAccountPoolTokenState(updatedTokenState)
	if err != nil {
		return AccountPoolAccountView{}, false, err
	}
	update := model.DB.Model(&model.AccountPoolAccount{}).
		Where(
			"id = ? AND pool_id = ? AND credential_config = ? AND token_state = ?",
			account.Id,
			pool.Id,
			account.CredentialConfig,
			account.TokenState,
		).
		Updates(map[string]any{
			"credential_config": encryptedCredential,
			"token_state":       encryptedTokenState,
			"updated_time":      common.GetTimestamp(),
		})
	if update.Error != nil {
		return AccountPoolAccountView{}, false, update.Error
	}
	if err := model.DB.Where("id = ? AND pool_id = ?", account.Id, pool.Id).First(&account).Error; err != nil {
		return AccountPoolAccountView{}, false, err
	}
	view, err := buildAccountPoolAccountView(account)
	return view, update.RowsAffected > 0, err
}

func (s AccountPoolService) ImportXAISSOAccounts(ctx context.Context, params AccountPoolXAISSOImportParams) (AccountPoolXAISSOImportResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	pool, err := getAccountPoolExistingPool(params.PoolID)
	if err != nil {
		return AccountPoolXAISSOImportResult{}, err
	}
	if pool.Platform != model.AccountPoolPlatformXAI {
		return AccountPoolXAISSOImportResult{}, errors.New("account pool is not an xai pool")
	}
	tokens := make([]string, 0, len(params.SSOTokens))
	seen := make(map[string]struct{}, len(params.SSOTokens))
	for _, raw := range params.SSOTokens {
		token := NormalizeXAISSOToken(raw)
		if token == "" {
			continue
		}
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	if len(tokens) == 0 {
		return AccountPoolXAISSOImportResult{}, errors.New("at least one xai sso token is required")
	}
	if len(tokens) > maxAccountPoolXAISSOImportAccounts {
		return AccountPoolXAISSOImportResult{}, fmt.Errorf("xai sso import supports at most %d accounts", maxAccountPoolXAISSOImportAccounts)
	}
	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(params.ProxyID, pool.DefaultProxyID)
	if err != nil {
		return AccountPoolXAISSOImportResult{}, err
	}
	result := AccountPoolXAISSOImportResult{
		Created: make([]AccountPoolAccountView, 0, len(tokens)),
		Errors:  make([]AccountPoolXAISSOImportError, 0),
	}
	batchCtx, cancel := context.WithTimeout(ctx, loadAccountPoolXAISSOImportTimeout())
	defer cancel()
	conversionResults := make([]accountPoolXAISSOConversionResult, len(tokens))
	jobs := make(chan int)
	workerCount := min(loadAccountPoolXAISSOImportConcurrency(), len(tokens))
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				itemCtx, itemCancel := context.WithTimeout(batchCtx, xaiSSOTimeout)
				info, conversionErr := accountPoolXAISSOConvert(itemCtx, tokens[index], proxyURL)
				itemCancel()
				conversionResults[index] = accountPoolXAISSOConversionResult{
					Info:      info,
					Err:       conversionErr,
					Attempted: true,
				}
			}
		}()
	}
	queueing := true
	for index := range tokens {
		if !queueing {
			break
		}
		select {
		case jobs <- index:
		case <-batchCtx.Done():
			queueing = false
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	for index, conversion := range conversionResults {
		if !conversion.Attempted || conversion.Err != nil || conversion.Info == nil {
			message := "xai sso conversion failed"
			if !conversion.Attempted || errors.Is(conversion.Err, context.DeadlineExceeded) {
				message = "xai sso conversion timed out"
			}
			result.Errors = append(result.Errors, AccountPoolXAISSOImportError{
				Index:   index + 1,
				Message: message,
			})
			continue
		}
		if batchCtx.Err() != nil {
			result.Errors = append(result.Errors, AccountPoolXAISSOImportError{
				Index:   index + 1,
				Message: "xai sso import timed out before account creation",
			})
			continue
		}
		info := conversion.Info
		identifier := strings.TrimSpace(info.Email)
		if identifier == "" {
			identifier = strings.TrimSpace(info.Subject)
		}
		name := accountPoolXAISSOAccountName(params.Name, identifier, index, len(tokens))
		created, createErr := s.CreateAccount(AccountPoolAccountCreateParams{
			PoolID:            params.PoolID,
			Name:              name,
			AccountIdentifier: identifier,
			Credential:        info.AccountPoolCredential(),
			TokenState:        info.AccountPoolTokenState(),
			Status:            params.Status,
			Priority:          params.Priority,
			Weight:            params.Weight,
			MaxConcurrency:    params.MaxConcurrency,
			MaxConcurrencySet: params.MaxConcurrencySet,
			ProxyID:           params.ProxyID,
			SupportedModels:   params.SupportedModels,
			ModelMapping:      params.ModelMapping,
		})
		if createErr != nil {
			result.Errors = append(result.Errors, AccountPoolXAISSOImportError{
				Index:   index + 1,
				Name:    name,
				Message: "create xai oauth account failed",
			})
			continue
		}
		result.Created = append(result.Created, created)
	}
	return result, nil
}

func loadAccountPoolXAISSOImportConcurrency() int {
	concurrency := common.GetEnvOrDefault(accountPoolXAISSOImportConcurrencyEnv, accountPoolXAISSOImportDefaultConcurrency)
	if concurrency <= 0 {
		return accountPoolXAISSOImportDefaultConcurrency
	}
	return min(concurrency, accountPoolXAISSOImportMaxConcurrency)
}

func loadAccountPoolXAISSOImportTimeout() time.Duration {
	seconds := common.GetEnvOrDefault(accountPoolXAISSOImportTimeoutEnv, int(accountPoolXAISSOImportDefaultTimeout/time.Second))
	if seconds < int(accountPoolXAISSOImportMinTimeout/time.Second) || seconds > int(accountPoolXAISSOImportMaxTimeout/time.Second) {
		return accountPoolXAISSOImportDefaultTimeout
	}
	return time.Duration(seconds) * time.Second
}

func accountPoolXAISSOAccountName(base string, identifier string, index int, total int) string {
	base = strings.TrimSpace(base)
	if base == "" {
		base = "Grok OAuth"
	}
	if identifier != "" {
		return base + " - " + identifier
	}
	if total > 1 {
		return fmt.Sprintf("%s %d", base, index+1)
	}
	return base
}
