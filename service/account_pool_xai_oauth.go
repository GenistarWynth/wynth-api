package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const maxAccountPoolXAISSOImportAccounts = 25

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
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(credential.Type), AccountPoolCredentialTypeOAuth) {
		return AccountPoolAccountView{}, errors.New("account is not an xai oauth account")
	}
	tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	refreshToken := strings.TrimSpace(credential.RefreshToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(tokenState.RefreshToken)
	}
	if refreshToken == "" {
		return AccountPoolAccountView{}, errors.New("xai oauth refresh_token is required")
	}
	proxyURL, err := ResolveAccountPoolRuntimeProxyURL(account.ProxyID, pool.DefaultProxyID)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	info, err := accountPoolXAIOAuthInfoRefresh(ctx, refreshToken, proxyURL, credential.ClientID)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	updatedCredential := mergeAccountPoolCredentialUpdate(credential, info.AccountPoolCredential())
	updatedTokenState := info.AccountPoolTokenState()
	updatedTokenState.Version = tokenState.Version + 1
	if updatedTokenState.Version <= 0 {
		updatedTokenState.Version = 1
	}
	encryptedCredential, err := EncryptAccountPoolCredentialConfig(updatedCredential)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	encryptedTokenState, err := EncryptAccountPoolTokenState(updatedTokenState)
	if err != nil {
		return AccountPoolAccountView{}, err
	}
	if err := model.DB.Model(&account).Updates(map[string]any{
		"credential_config": encryptedCredential,
		"token_state":       encryptedTokenState,
		"updated_time":      common.GetTimestamp(),
	}).Error; err != nil {
		return AccountPoolAccountView{}, err
	}
	if err := model.DB.First(&account, accountID).Error; err != nil {
		return AccountPoolAccountView{}, err
	}
	return buildAccountPoolAccountView(account)
}

func (s AccountPoolService) ImportXAISSOAccounts(ctx context.Context, params AccountPoolXAISSOImportParams) (AccountPoolXAISSOImportResult, error) {
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
	for index, token := range tokens {
		info, conversionErr := accountPoolXAISSOConvert(ctx, token, proxyURL)
		if conversionErr != nil {
			result.Errors = append(result.Errors, AccountPoolXAISSOImportError{
				Index:   index + 1,
				Message: "xai sso conversion failed",
			})
			continue
		}
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
