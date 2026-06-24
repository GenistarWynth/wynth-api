package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gopkg.in/yaml.v3"
)

const (
	accountPoolImportFormatSub2API = "sub2api"
	accountPoolImportFormatCPA     = "cpa"
)

type AccountPoolAccountImportDefaults struct {
	Status            string
	Priority          int64
	Weight            uint
	MaxConcurrency    int
	MaxConcurrencySet bool
	ProxyID           int
	SupportedModels   []string
	ModelMapping      map[string]string
}

type AccountPoolAccountImportParams struct {
	PoolID   int
	Format   string
	Content  string
	Defaults AccountPoolAccountImportDefaults
	DryRun   bool
}

type AccountPoolAccountImportError struct {
	Index   int    `json:"index,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

type AccountPoolAccountImportResult struct {
	Imported     int                             `json:"imported"`
	Skipped      int                             `json:"skipped"`
	Failed       int                             `json:"failed"`
	ProxyCreated int                             `json:"proxy_created"`
	ProxyReused  int                             `json:"proxy_reused"`
	Accounts     []AccountPoolAccountView        `json:"accounts,omitempty"`
	Errors       []AccountPoolAccountImportError `json:"errors,omitempty"`
}

type accountPoolImportCandidate struct {
	Index  int
	Name   string
	Params AccountPoolAccountCreateParams
}

type accountPoolImportProxyStats struct {
	Created int
	Reused  int
}

type accountPoolSub2APIDataPayload struct {
	Type       string                      `json:"type,omitempty"`
	Version    int                         `json:"version,omitempty"`
	ExportedAt string                      `json:"exported_at"`
	Proxies    []accountPoolSub2APIProxy   `json:"proxies"`
	Accounts   []accountPoolSub2APIAccount `json:"accounts"`
}

type accountPoolSub2APIDataWrapper struct {
	Data accountPoolSub2APIDataPayload `json:"data"`
}

type accountPoolSub2APIProxy struct {
	ProxyKey        string `json:"proxy_key"`
	Name            string `json:"name"`
	Protocol        string `json:"protocol"`
	Host            string `json:"host"`
	Port            int    `json:"port"`
	Username        string `json:"username,omitempty"`
	Password        string `json:"password,omitempty"`
	Status          string `json:"status"`
	FallbackMode    string `json:"fallback_mode,omitempty"`
	BackupProxyName string `json:"backup_proxy_name,omitempty"`
	ExpiryWarnDays  int    `json:"expiry_warn_days,omitempty"`
}

type accountPoolSub2APIAccount struct {
	Name               string         `json:"name"`
	Notes              *string        `json:"notes,omitempty"`
	Platform           string         `json:"platform"`
	Type               string         `json:"type"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra,omitempty"`
	ProxyKey           *string        `json:"proxy_key,omitempty"`
	Concurrency        *int           `json:"concurrency"`
	Priority           int64          `json:"priority"`
	RateMultiplier     *float64       `json:"rate_multiplier,omitempty"`
	ExpiresAt          *int64         `json:"expires_at,omitempty"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired,omitempty"`
}

type accountPoolCPAConfigPayload struct {
	CodexAPIKeys []accountPoolCPACodexKey `yaml:"codex-api-key" json:"codex-api-key"`
}

type accountPoolCPACodexKey struct {
	APIKey         string                     `yaml:"api-key" json:"api-key"`
	Priority       int64                      `yaml:"priority,omitempty" json:"priority,omitempty"`
	Prefix         string                     `yaml:"prefix,omitempty" json:"prefix,omitempty"`
	BaseURL        string                     `yaml:"base-url" json:"base-url"`
	Websockets     bool                       `yaml:"websockets,omitempty" json:"websockets,omitempty"`
	ProxyURL       string                     `yaml:"proxy-url" json:"proxy-url"`
	Models         []accountPoolCPACodexModel `yaml:"models" json:"models"`
	Headers        map[string]string          `yaml:"headers,omitempty" json:"headers,omitempty"`
	ExcludedModels []string                   `yaml:"excluded-models,omitempty" json:"excluded-models,omitempty"`
	DisableCooling bool                       `yaml:"disable-cooling,omitempty" json:"disable-cooling,omitempty"`
}

type accountPoolCPACodexModel struct {
	Name  string `yaml:"name" json:"name"`
	Alias string `yaml:"alias" json:"alias"`
}

func (s AccountPoolService) ImportAccounts(params AccountPoolAccountImportParams) (AccountPoolAccountImportResult, error) {
	result := AccountPoolAccountImportResult{}
	if _, err := getAccountPoolExistingPool(params.PoolID); err != nil {
		return result, err
	}
	if strings.TrimSpace(params.Content) == "" {
		return result, errors.New("account import content is required")
	}

	format, err := normalizeAccountPoolImportFormat(params.Format)
	if err != nil {
		return result, err
	}
	if err := validateAccountPoolProxyReference(params.Defaults.ProxyID); err != nil {
		return result, err
	}

	var candidates []accountPoolImportCandidate
	var stats accountPoolImportProxyStats
	var parseErrors []AccountPoolAccountImportError
	switch format {
	case accountPoolImportFormatSub2API:
		candidates, stats, parseErrors, err = s.parseSub2APIImport(params)
	case accountPoolImportFormatCPA:
		candidates, stats, parseErrors, err = s.parseCPAImport(params)
	default:
		err = errors.New("unsupported account import format")
	}
	if err != nil {
		return result, err
	}
	result.ProxyCreated += stats.Created
	result.ProxyReused += stats.Reused
	result.Errors = append(result.Errors, parseErrors...)
	result.Skipped += len(parseErrors)

	existingKeys, err := accountPoolExistingImportDuplicateKeys(params.PoolID)
	if err != nil {
		return result, err
	}
	seenKeys := make(map[string]struct{})
	for _, candidate := range candidates {
		accountPoolApplyImportDefaults(&candidate.Params, params.Defaults)
		accountPoolNormalizeImportAccountParams(&candidate.Params)
		duplicateKeys := accountPoolImportDuplicateKeys(candidate.Params)
		if len(duplicateKeys) == 0 {
			result.Skipped++
			result.Errors = append(result.Errors, AccountPoolAccountImportError{
				Index:   candidate.Index,
				Name:    candidate.Name,
				Message: "account import entry has no stable credential or account identifier",
			})
			continue
		}
		if accountPoolImportHasDuplicateKey(duplicateKeys, existingKeys, seenKeys) {
			result.Skipped++
			result.Errors = append(result.Errors, AccountPoolAccountImportError{
				Index:   candidate.Index,
				Name:    candidate.Name,
				Message: "duplicate account skipped",
			})
			continue
		}
		for _, key := range duplicateKeys {
			seenKeys[key] = struct{}{}
		}
		if params.DryRun {
			result.Imported++
			continue
		}
		account, err := s.CreateAccount(candidate.Params)
		if err != nil {
			result.Failed++
			result.Errors = append(result.Errors, AccountPoolAccountImportError{
				Index:   candidate.Index,
				Name:    candidate.Name,
				Message: err.Error(),
			})
			continue
		}
		result.Imported++
		result.Accounts = append(result.Accounts, account)
	}
	if result.Imported == 0 && result.Skipped == 0 && result.Failed == 0 {
		return result, errors.New("no importable account entries found")
	}
	return result, nil
}

func normalizeAccountPoolImportFormat(format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	switch format {
	case "sub2api", "sub2api-data", "sub2api-bundle":
		return accountPoolImportFormatSub2API, nil
	case "cpa", "cliproxyapi", "cli-proxy-api", "cli_proxy_api":
		return accountPoolImportFormatCPA, nil
	default:
		return "", errors.New("account import format must be sub2api or cpa")
	}
}

func (s AccountPoolService) parseSub2APIImport(params AccountPoolAccountImportParams) ([]accountPoolImportCandidate, accountPoolImportProxyStats, []AccountPoolAccountImportError, error) {
	var payload accountPoolSub2APIDataPayload
	if err := common.UnmarshalJsonStr(params.Content, &payload); err != nil {
		return nil, accountPoolImportProxyStats{}, nil, err
	}
	if len(payload.Accounts) == 0 {
		var wrapper accountPoolSub2APIDataWrapper
		if err := common.UnmarshalJsonStr(params.Content, &wrapper); err == nil && len(wrapper.Data.Accounts) > 0 {
			payload = wrapper.Data
		}
	}

	proxyIDs := make(map[string]int)
	proxyErrors := make(map[string]string)
	stats := accountPoolImportProxyStats{}
	for _, proxy := range payload.Proxies {
		key := strings.TrimSpace(proxy.ProxyKey)
		if key == "" {
			key = accountPoolSub2APIProxyDedupeKey(proxy)
		}
		if key == "" {
			continue
		}
		if params.DryRun {
			proxyIDs[key] = 0
			stats.Created++
			continue
		}
		view, created, err := s.getOrCreateImportProxy(AccountPoolProxyCreateParams{
			Name:     accountPoolImportDefaultString(proxy.Name, fmt.Sprintf("sub2api proxy %s", key)),
			Protocol: proxy.Protocol,
			Host:     proxy.Host,
			Port:     proxy.Port,
			Username: proxy.Username,
			Password: proxy.Password,
			Status:   normalizeAccountPoolImportProxyStatus(proxy.Status),
		})
		if err != nil {
			proxyErrors[key] = err.Error()
			continue
		}
		proxyIDs[key] = view.Id
		if created {
			stats.Created++
		} else {
			stats.Reused++
		}
	}

	candidates := make([]accountPoolImportCandidate, 0, len(payload.Accounts))
	importErrors := make([]AccountPoolAccountImportError, 0)
	for index, account := range payload.Accounts {
		candidate, ok, message := accountPoolSub2APIAccountCandidate(params.PoolID, account)
		if !ok {
			importErrors = append(importErrors, AccountPoolAccountImportError{
				Index:   index,
				Name:    account.Name,
				Message: message,
			})
			continue
		}
		if account.ProxyKey != nil {
			proxyKey := strings.TrimSpace(*account.ProxyKey)
			if proxyKey != "" {
				proxyID, found := proxyIDs[proxyKey]
				if !found {
					message := "referenced proxy_key is missing from import"
					if proxyError := proxyErrors[proxyKey]; proxyError != "" {
						message = "referenced proxy_key could not be imported: " + proxyError
					}
					importErrors = append(importErrors, AccountPoolAccountImportError{
						Index:   index,
						Name:    account.Name,
						Message: message,
					})
					continue
				}
				if proxyID > 0 {
					candidate.Params.ProxyID = proxyID
				}
			}
		}
		candidate.Index = index
		candidates = append(candidates, candidate)
	}
	return candidates, stats, importErrors, nil
}

func accountPoolSub2APIAccountCandidate(poolID int, account accountPoolSub2APIAccount) (accountPoolImportCandidate, bool, string) {
	platform := strings.ToLower(strings.TrimSpace(account.Platform))
	if platform != "" && platform != "openai" {
		return accountPoolImportCandidate{}, false, "unsupported sub2api account platform"
	}

	credentialType := strings.ToLower(strings.TrimSpace(account.Type))
	apiKey := accountPoolImportStringFromMap(account.Credentials, "api_key", "key")
	refreshToken := accountPoolImportStringFromMap(account.Credentials, "refresh_token")
	accessToken := accountPoolImportStringFromMap(account.Credentials, "access_token")
	email := accountPoolImportStringFromMap(account.Credentials, "email")
	accountIdentifier := accountPoolImportStringFromMap(account.Credentials, "chatgpt_account_id", "account_id", "id")
	expiresAt := accountPoolImportInt64FromMap(account.Credentials, "expires_at", "expired")
	if expiresAt == 0 && account.ExpiresAt != nil {
		expiresAt = *account.ExpiresAt
	}

	candidate := accountPoolImportCandidate{
		Name: strings.TrimSpace(account.Name),
		Params: AccountPoolAccountCreateParams{
			PoolID:            poolID,
			Name:              strings.TrimSpace(account.Name),
			AccountIdentifier: accountIdentifier,
			Priority:          account.Priority,
			SupportedModels: accountPoolFirstStringSlice(
				accountPoolImportStringSliceFromMap(account.Extra, "supported_models", "models"),
				accountPoolImportStringSliceFromMap(account.Credentials, "supported_models", "models"),
			),
			ModelMapping: accountPoolFirstStringMap(
				accountPoolImportStringMapFromMap(account.Extra, "model_mapping"),
				accountPoolImportStringMapFromMap(account.Credentials, "model_mapping"),
			),
		},
	}
	if account.Concurrency != nil {
		candidate.Params.MaxConcurrency = *account.Concurrency
		candidate.Params.MaxConcurrencySet = true
	}
	if candidate.Params.Name == "" {
		candidate.Params.Name = accountPoolImportDefaultString(email, accountPoolImportDefaultString(accountIdentifier, "sub2api account"))
		candidate.Name = candidate.Params.Name
	}

	switch {
	case apiKey != "" || credentialType == AccountPoolCredentialTypeAPIKey:
		if apiKey == "" {
			return accountPoolImportCandidate{}, false, "sub2api api_key account is missing api_key"
		}
		candidate.Params.Credential = AccountPoolCredentialConfig{
			Type:   AccountPoolCredentialTypeAPIKey,
			APIKey: apiKey,
		}
	case refreshToken != "" || accessToken != "" || email != "":
		candidate.Params.Credential = AccountPoolCredentialConfig{
			Type:         AccountPoolCredentialTypeOAuth,
			Email:        email,
			RefreshToken: refreshToken,
		}
		candidate.Params.TokenState = AccountPoolTokenState{
			AccessToken:  accessToken,
			RefreshToken: refreshToken,
			ExpiresAt:    expiresAt,
		}
	default:
		return accountPoolImportCandidate{}, false, "sub2api account has no supported credential"
	}

	return candidate, true, ""
}
func (s AccountPoolService) parseCPAImport(params AccountPoolAccountImportParams) ([]accountPoolImportCandidate, accountPoolImportProxyStats, []AccountPoolAccountImportError, error) {
	var payload accountPoolCPAConfigPayload
	if err := yaml.Unmarshal([]byte(params.Content), &payload); err == nil && len(payload.CodexAPIKeys) > 0 {
		return s.parseCPAConfigImport(params, payload)
	}
	return s.parseCPAAuthJSONImport(params)
}

func (s AccountPoolService) parseCPAConfigImport(params AccountPoolAccountImportParams, payload accountPoolCPAConfigPayload) ([]accountPoolImportCandidate, accountPoolImportProxyStats, []AccountPoolAccountImportError, error) {
	candidates := make([]accountPoolImportCandidate, 0, len(payload.CodexAPIKeys))
	importErrors := make([]AccountPoolAccountImportError, 0)
	stats := accountPoolImportProxyStats{}
	for index, key := range payload.CodexAPIKeys {
		apiKey := strings.TrimSpace(key.APIKey)
		if apiKey == "" {
			importErrors = append(importErrors, AccountPoolAccountImportError{
				Index:   index,
				Message: "CPA codex-api-key entry is missing api-key",
			})
			continue
		}
		proxyID := params.Defaults.ProxyID
		if strings.TrimSpace(key.ProxyURL) != "" && !params.DryRun {
			proxyParams, ok, err := accountPoolProxyCreateParamsFromURL(key.ProxyURL, accountPoolCPAProxyName(key, apiKey))
			if err != nil {
				importErrors = append(importErrors, AccountPoolAccountImportError{
					Index:   index,
					Name:    accountPoolCPACodexAccountName(key, apiKey),
					Message: err.Error(),
				})
				continue
			}
			if ok {
				view, created, err := s.getOrCreateImportProxy(proxyParams)
				if err != nil {
					importErrors = append(importErrors, AccountPoolAccountImportError{
						Index:   index,
						Name:    accountPoolCPACodexAccountName(key, apiKey),
						Message: err.Error(),
					})
					continue
				}
				proxyID = view.Id
				if created {
					stats.Created++
				} else {
					stats.Reused++
				}
			}
		}

		supportedModels, modelMapping := accountPoolCPAModelPolicy(key)
		candidate := accountPoolImportCandidate{
			Index: index,
			Name:  accountPoolCPACodexAccountName(key, apiKey),
			Params: AccountPoolAccountCreateParams{
				PoolID:          params.PoolID,
				Name:            accountPoolCPACodexAccountName(key, apiKey),
				Credential:      AccountPoolCredentialConfig{Type: AccountPoolCredentialTypeAPIKey, APIKey: apiKey},
				Priority:        key.Priority,
				ProxyID:         proxyID,
				SupportedModels: supportedModels,
				ModelMapping:    modelMapping,
			},
		}
		candidates = append(candidates, candidate)
	}
	return candidates, stats, importErrors, nil
}

func (s AccountPoolService) parseCPAAuthJSONImport(params AccountPoolAccountImportParams) ([]accountPoolImportCandidate, accountPoolImportProxyStats, []AccountPoolAccountImportError, error) {
	var raw any
	if err := common.UnmarshalJsonStr(params.Content, &raw); err != nil {
		return nil, accountPoolImportProxyStats{}, nil, err
	}
	objects := accountPoolImportFlattenObjects(raw)
	candidates := make([]accountPoolImportCandidate, 0, len(objects))
	importErrors := make([]AccountPoolAccountImportError, 0)
	stats := accountPoolImportProxyStats{}
	for index, object := range objects {
		candidate, proxyURL, ok, message := accountPoolCPAAuthCandidate(params.PoolID, index, object)
		if !ok {
			importErrors = append(importErrors, AccountPoolAccountImportError{
				Index:   index,
				Name:    accountPoolImportStringFromMap(object, "label", "name", "email", "id"),
				Message: message,
			})
			continue
		}
		if proxyURL != "" && !params.DryRun {
			proxyParams, ok, err := accountPoolProxyCreateParamsFromURL(proxyURL, "CPA "+candidate.Params.Name)
			if err != nil {
				importErrors = append(importErrors, AccountPoolAccountImportError{
					Index:   index,
					Name:    candidate.Name,
					Message: err.Error(),
				})
				continue
			}
			if ok {
				view, created, err := s.getOrCreateImportProxy(proxyParams)
				if err != nil {
					importErrors = append(importErrors, AccountPoolAccountImportError{
						Index:   index,
						Name:    candidate.Name,
						Message: err.Error(),
					})
					continue
				}
				candidate.Params.ProxyID = view.Id
				if created {
					stats.Created++
				} else {
					stats.Reused++
				}
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, stats, importErrors, nil
}

func accountPoolCPAAuthCandidate(poolID int, index int, object map[string]any) (accountPoolImportCandidate, string, bool, string) {
	metadata := accountPoolImportMapValue(object["metadata"])
	if len(metadata) == 0 {
		metadata = object
	}
	provider := strings.ToLower(accountPoolImportDefaultString(
		accountPoolImportStringFromMap(object, "provider", "type"),
		accountPoolImportStringFromMap(metadata, "provider", "type"),
	))
	if provider != "" && provider != "codex" && provider != "openai" {
		return accountPoolImportCandidate{}, "", false, "unsupported CPA auth provider"
	}

	email := accountPoolImportStringFromMap(metadata, "email")
	accountIdentifier := accountPoolImportStringFromMap(metadata, "account_id", "chatgpt_account_id", "id")
	accessToken := accountPoolImportStringFromMap(metadata, "access_token")
	refreshToken := accountPoolImportStringFromMap(metadata, "refresh_token")
	if accessToken == "" && refreshToken == "" {
		return accountPoolImportCandidate{}, "", false, "CPA auth entry has no access_token or refresh_token"
	}
	name := accountPoolImportDefaultString(
		accountPoolImportStringFromMap(object, "label", "name"),
		accountPoolImportDefaultString(email, accountPoolImportDefaultString(accountIdentifier, fmt.Sprintf("CPA auth %d", index+1))),
	)
	proxyURL := accountPoolImportDefaultString(
		accountPoolImportStringFromMap(object, "proxy_url", "proxy-url"),
		accountPoolImportStringFromMap(metadata, "proxy_url", "proxy-url"),
	)

	candidate := accountPoolImportCandidate{
		Index: index,
		Name:  name,
		Params: AccountPoolAccountCreateParams{
			PoolID:            poolID,
			Name:              name,
			AccountIdentifier: accountIdentifier,
			Credential: AccountPoolCredentialConfig{
				Type:         AccountPoolCredentialTypeOAuth,
				Email:        email,
				RefreshToken: refreshToken,
			},
			TokenState: AccountPoolTokenState{
				AccessToken:  accessToken,
				RefreshToken: refreshToken,
				ExpiresAt: accountPoolImportFirstInt64(
					accountPoolImportInt64FromMap(metadata, "expires_at"),
					accountPoolImportInt64FromMap(metadata, "expired"),
				),
			},
		},
	}
	return candidate, proxyURL, true, ""
}

func (s AccountPoolService) getOrCreateImportProxy(params AccountPoolProxyCreateParams) (AccountPoolProxyView, bool, error) {
	params.Name = strings.TrimSpace(params.Name)
	params.Protocol = strings.ToLower(strings.TrimSpace(params.Protocol))
	params.Host = strings.TrimSpace(params.Host)
	params.Username = strings.TrimSpace(params.Username)
	if params.Status == "" {
		params.Status = model.AccountPoolProxyStatusEnabled
	}
	if params.Protocol == "" || params.Host == "" || params.Port <= 0 {
		return AccountPoolProxyView{}, false, errors.New("account import proxy is missing protocol, host, or port")
	}

	var proxies []model.AccountPoolProxy
	if err := model.DB.Where(
		"status <> ? AND protocol = ? AND host = ? AND port = ? AND username = ?",
		model.AccountPoolProxyStatusDeleted,
		params.Protocol,
		params.Host,
		params.Port,
		params.Username,
	).Order("id asc").Find(&proxies).Error; err != nil {
		return AccountPoolProxyView{}, false, err
	}
	for _, proxy := range proxies {
		if accountPoolStoredProxyPasswordMatches(proxy, params.Password) {
			view, err := buildAccountPoolProxyView(proxy)
			return view, false, err
		}
	}
	view, err := s.CreateProxy(params)
	return view, true, err
}

func accountPoolStoredProxyPasswordMatches(proxy model.AccountPoolProxy, password string) bool {
	password = strings.TrimSpace(password)
	if strings.TrimSpace(proxy.Password) == "" {
		return password == ""
	}
	config, err := DecryptAccountPoolProxyAuthConfig(proxy.Password)
	if err != nil {
		return false
	}
	return strings.TrimSpace(config.Password) == password
}

func accountPoolApplyImportDefaults(params *AccountPoolAccountCreateParams, defaults AccountPoolAccountImportDefaults) {
	if strings.TrimSpace(params.Status) == "" {
		params.Status = defaults.Status
	}
	if params.Priority == 0 {
		params.Priority = defaults.Priority
	}
	if params.Weight == 0 {
		params.Weight = defaults.Weight
	}
	if params.MaxConcurrency == 0 && !params.MaxConcurrencySet {
		params.MaxConcurrency = defaults.MaxConcurrency
		params.MaxConcurrencySet = defaults.MaxConcurrencySet
	}
	if params.ProxyID == 0 {
		params.ProxyID = defaults.ProxyID
	}
	if len(params.SupportedModels) == 0 {
		params.SupportedModels = append([]string(nil), defaults.SupportedModels...)
	}
	if len(params.ModelMapping) == 0 && len(defaults.ModelMapping) > 0 {
		params.ModelMapping = accountPoolCopyStringMap(defaults.ModelMapping)
	}
}

func accountPoolNormalizeImportAccountParams(params *AccountPoolAccountCreateParams) {
	params.Name = strings.TrimSpace(params.Name)
	params.AccountIdentifier = strings.TrimSpace(params.AccountIdentifier)
	params.Status = normalizeAccountPoolImportAccountStatus(params.Status)
	params.Credential.Type = normalizeAccountPoolImportCredentialType(params.Credential.Type)
	params.Credential.APIKey = strings.TrimSpace(params.Credential.APIKey)
	params.Credential.Email = strings.TrimSpace(params.Credential.Email)
	params.Credential.RefreshToken = strings.TrimSpace(params.Credential.RefreshToken)
	params.TokenState.AccessToken = strings.TrimSpace(params.TokenState.AccessToken)
	params.TokenState.RefreshToken = strings.TrimSpace(params.TokenState.RefreshToken)
	params.SupportedModels = accountPoolCleanStringSlice(params.SupportedModels)
	params.ModelMapping = accountPoolCleanStringMap(params.ModelMapping)
}

func normalizeAccountPoolImportAccountStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "", "active", "ok", "healthy", model.AccountPoolAccountStatusEnabled:
		return model.AccountPoolAccountStatusEnabled
	case "disabled", "disable", "paused":
		return model.AccountPoolAccountStatusDisabled
	case "expired":
		return model.AccountPoolAccountStatusExpired
	default:
		return status
	}
}

func normalizeAccountPoolImportProxyStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "", "active", "ok", "healthy", model.AccountPoolProxyStatusEnabled:
		return model.AccountPoolProxyStatusEnabled
	case "disabled", "disable", "paused", "expired", "error":
		return model.AccountPoolProxyStatusDisabled
	default:
		return status
	}
}

func normalizeAccountPoolImportCredentialType(credentialType string) string {
	credentialType = strings.ToLower(strings.TrimSpace(credentialType))
	switch credentialType {
	case AccountPoolCredentialTypeAPIKey, "apikey", "api-key":
		return AccountPoolCredentialTypeAPIKey
	case AccountPoolCredentialTypeOAuth, "codex", "openai":
		return AccountPoolCredentialTypeOAuth
	default:
		return credentialType
	}
}

func accountPoolExistingImportDuplicateKeys(poolID int) (map[string]struct{}, error) {
	var accounts []model.AccountPoolAccount
	if err := model.DB.Where("pool_id = ? AND status <> ?", poolID, model.AccountPoolAccountStatusDeleted).Find(&accounts).Error; err != nil {
		return nil, err
	}
	keys := make(map[string]struct{})
	for _, account := range accounts {
		if key := accountPoolImportIdentifierKey(account.AccountIdentifier); key != "" {
			keys[key] = struct{}{}
		}
		credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
		if err == nil {
			for _, key := range accountPoolImportCredentialKeys(credential) {
				keys[key] = struct{}{}
			}
		}
		tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
		if err == nil {
			for _, key := range accountPoolImportTokenKeys(tokenState) {
				keys[key] = struct{}{}
			}
		}
	}
	return keys, nil
}

func accountPoolImportDuplicateKeys(params AccountPoolAccountCreateParams) []string {
	keys := make([]string, 0, 4)
	if key := accountPoolImportIdentifierKey(params.AccountIdentifier); key != "" {
		keys = append(keys, key)
	}
	keys = append(keys, accountPoolImportCredentialKeys(params.Credential)...)
	keys = append(keys, accountPoolImportTokenKeys(params.TokenState)...)
	return accountPoolUniqueStrings(keys)
}

func accountPoolImportCredentialKeys(credential AccountPoolCredentialConfig) []string {
	keys := make([]string, 0, 2)
	if key := accountPoolImportSecretKey("api", credential.APIKey); key != "" {
		keys = append(keys, key)
	}
	if key := accountPoolImportSecretKey("refresh", credential.RefreshToken); key != "" {
		keys = append(keys, key)
	}
	return keys
}

func accountPoolImportTokenKeys(tokenState AccountPoolTokenState) []string {
	keys := make([]string, 0, 2)
	if key := accountPoolImportSecretKey("access", tokenState.AccessToken); key != "" {
		keys = append(keys, key)
	}
	if key := accountPoolImportSecretKey("refresh", tokenState.RefreshToken); key != "" {
		keys = append(keys, key)
	}
	return keys
}

func accountPoolImportIdentifierKey(identifier string) string {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if identifier == "" {
		return ""
	}
	return "identifier:" + identifier
}

func accountPoolImportSecretKey(prefix string, secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	return prefix + ":" + accountPoolImportHash(secret)
}

func accountPoolImportHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func accountPoolImportHasDuplicateKey(keys []string, existing map[string]struct{}, seen map[string]struct{}) bool {
	for _, key := range keys {
		if _, ok := existing[key]; ok {
			return true
		}
		if _, ok := seen[key]; ok {
			return true
		}
	}
	return false
}

func accountPoolSub2APIProxyDedupeKey(proxy accountPoolSub2APIProxy) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(proxy.Protocol)),
		strings.TrimSpace(proxy.Host),
		strconv.Itoa(proxy.Port),
		strings.TrimSpace(proxy.Username),
		strings.TrimSpace(proxy.Password),
	}
	var builder strings.Builder
	for _, part := range parts {
		builder.WriteString(strconv.Itoa(len(part)))
		builder.WriteByte(':')
		builder.WriteString(part)
		builder.WriteByte(';')
	}
	return accountPoolImportHash(builder.String())
}

func accountPoolProxyCreateParamsFromURL(rawURL string, name string) (AccountPoolProxyCreateParams, bool, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.EqualFold(rawURL, "direct") {
		return AccountPoolProxyCreateParams{}, false, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return AccountPoolProxyCreateParams{}, false, err
	}
	protocol := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	host := strings.TrimSpace(parsed.Hostname())
	if protocol == "" || host == "" {
		return AccountPoolProxyCreateParams{}, false, errors.New("proxy-url must include scheme and host")
	}
	port, err := accountPoolProxyPortFromURL(protocol, parsed.Port())
	if err != nil {
		return AccountPoolProxyCreateParams{}, false, err
	}
	username := ""
	password := ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	return AccountPoolProxyCreateParams{
		Name:     accountPoolImportDefaultString(name, host),
		Protocol: protocol,
		Host:     host,
		Port:     port,
		Username: username,
		Password: password,
		Status:   model.AccountPoolProxyStatusEnabled,
	}, true, nil
}

func accountPoolProxyPortFromURL(protocol string, rawPort string) (int, error) {
	if rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port <= 0 {
			return 0, errors.New("proxy-url port is invalid")
		}
		return port, nil
	}
	switch protocol {
	case "http":
		return 80, nil
	case "https":
		return 443, nil
	case "socks", "socks5":
		return 1080, nil
	default:
		return 0, errors.New("proxy-url port is required")
	}
}

func accountPoolCPACodexAccountName(key accountPoolCPACodexKey, apiKey string) string {
	prefix := strings.TrimSpace(key.Prefix)
	if prefix != "" {
		return "CPA / " + prefix + " / " + MaskAccountPoolSecretValue(apiKey)
	}
	return "CPA / " + MaskAccountPoolSecretValue(apiKey)
}

func accountPoolCPAProxyName(key accountPoolCPACodexKey, apiKey string) string {
	prefix := strings.TrimSpace(key.Prefix)
	if prefix != "" {
		return "CPA proxy / " + prefix
	}
	return "CPA proxy / " + MaskAccountPoolSecretValue(apiKey)
}

func accountPoolCPAModelPolicy(key accountPoolCPACodexKey) ([]string, map[string]string) {
	excluded := make(map[string]struct{})
	for _, modelName := range key.ExcludedModels {
		modelName = strings.ToLower(strings.TrimSpace(modelName))
		if modelName != "" {
			excluded[modelName] = struct{}{}
		}
	}
	supported := make([]string, 0, len(key.Models))
	mapping := make(map[string]string)
	for _, modelConfig := range key.Models {
		upstream := strings.TrimSpace(modelConfig.Name)
		if upstream == "" {
			continue
		}
		public := strings.TrimSpace(modelConfig.Alias)
		if public == "" {
			public = upstream
		}
		if _, ok := excluded[strings.ToLower(public)]; ok {
			continue
		}
		if _, ok := excluded[strings.ToLower(upstream)]; ok {
			continue
		}
		clientModel := accountPoolCPAClientModelName(key.Prefix, public)
		supported = append(supported, clientModel)
		mapping[clientModel] = upstream
	}
	return accountPoolUniqueStrings(supported), mapping
}

func accountPoolCPAClientModelName(prefix string, modelName string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	modelName = strings.Trim(strings.TrimSpace(modelName), "/")
	if prefix == "" {
		return modelName
	}
	return prefix + "/" + modelName
}

func accountPoolImportFlattenObjects(raw any) []map[string]any {
	switch value := raw.(type) {
	case []any:
		objects := make([]map[string]any, 0, len(value))
		for _, item := range value {
			if object := accountPoolImportMapValue(item); len(object) > 0 {
				objects = append(objects, object)
			}
		}
		return objects
	case map[string]any:
		for _, key := range []string{"auths", "accounts", "items", "data"} {
			if nested, ok := value[key].([]any); ok {
				return accountPoolImportFlattenObjects(nested)
			}
		}
		return []map[string]any{value}
	default:
		return nil
	}
}

func accountPoolImportStringFromMap(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text := accountPoolImportStringValue(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func accountPoolImportInt64FromMap(values map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if parsed := accountPoolImportInt64Value(value); parsed != 0 {
				return parsed
			}
		}
	}
	return 0
}

func accountPoolImportStringSliceFromMap(values map[string]any, keys ...string) []string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if parsed := accountPoolImportStringSliceValue(value); len(parsed) > 0 {
				return parsed
			}
		}
	}
	return nil
}

func accountPoolImportStringMapFromMap(values map[string]any, keys ...string) map[string]string {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if parsed := accountPoolImportStringMapValue(value); len(parsed) > 0 {
				return parsed
			}
		}
	}
	return nil
}

func accountPoolImportStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return ""
	}
}

func accountPoolImportInt64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case int32:
		return int64(typed)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case string:
		typed = strings.TrimSpace(typed)
		if typed == "" {
			return 0
		}
		if parsed, err := strconv.ParseInt(typed, 10, 64); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(typed, 64); err == nil {
			return int64(parsed)
		}
		if parsed, err := time.Parse(time.RFC3339, typed); err == nil {
			return parsed.Unix()
		}
		return 0
	default:
		return 0
	}
}

func accountPoolImportMapValue(value any) map[string]any {
	if value == nil {
		return nil
	}
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func accountPoolImportStringSliceValue(value any) []string {
	switch typed := value.(type) {
	case []string:
		return accountPoolCleanStringSlice(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := accountPoolImportStringValue(item); text != "" {
				values = append(values, text)
			}
		}
		return accountPoolCleanStringSlice(values)
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		parts := strings.Split(typed, ",")
		return accountPoolCleanStringSlice(parts)
	default:
		return nil
	}
}

func accountPoolImportStringMapValue(value any) map[string]string {
	switch typed := value.(type) {
	case map[string]string:
		return accountPoolCleanStringMap(typed)
	case map[string]any:
		values := make(map[string]string)
		for key, item := range typed {
			text := accountPoolImportStringValue(item)
			if strings.TrimSpace(key) != "" && text != "" {
				values[strings.TrimSpace(key)] = text
			}
		}
		return accountPoolCleanStringMap(values)
	default:
		return nil
	}
}

func accountPoolFirstStringSlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func accountPoolFirstStringMap(values ...map[string]string) map[string]string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func accountPoolImportFirstInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func accountPoolImportDefaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func accountPoolCleanStringSlice(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func accountPoolCleanStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cleaned := make(map[string]string)
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		cleaned[key] = value
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func accountPoolCopyStringMap(values map[string]string) map[string]string {
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func accountPoolUniqueStrings(values []string) []string {
	return accountPoolCleanStringSlice(values)
}

func accountPoolSingleOptionalString(value string) []string {
	if value == "" {
		return nil
	}
	return []string{value}
}
