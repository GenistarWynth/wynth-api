package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// ExportAccounts serializes every (non-deleted) account in a pool, plus the proxies
// those accounts reference, into the same `sub2api-data` JSON shape the importer
// consumes (accountPoolSub2APIDataPayload). This lets a full export round-trip back
// through ImportAccounts.
//
// When includeSecrets is true the real api_key / refresh_token / access_token / expires_at
// and proxy passwords are emitted, so the export re-imports as usable credentials.
//
// When includeSecrets is false (the default) every secret value is masked with
// MaskAccountPoolSecretValue (e.g. "sk-a...3xyz" or "***"). A redacted export is for
// inspection only: the masked values are NOT usable credentials, so it will not
// re-import working accounts. Non-secret fields (email, oauth_type, project_id,
// account_id, platform, supported_models, model_mapping) are always emitted.
// ExportAccounts returns the export payload plus a count of accounts that were
// SKIPPED because their stored credential could not be decrypted. The error
// return is reserved for infrastructure failures (pool lookup, DB query); a
// single corrupt credential blob must NOT block exporting the rest of the pool
// (mirrors the importer's skip-and-continue), so per-account decrypt failures are
// logged + counted, not fatal.
func (s AccountPoolService) ExportAccounts(poolID int, includeSecrets bool) (accountPoolSub2APIDataPayload, int, error) {
	payload := accountPoolSub2APIDataPayload{
		Type:       "sub2api-data",
		Version:    1,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Proxies:    []accountPoolSub2APIProxy{},
		Accounts:   []accountPoolSub2APIAccount{},
	}

	pool, err := getAccountPoolExistingPool(poolID)
	if err != nil {
		return payload, 0, err
	}
	platform, err := normalizeAccountPoolPlatform(pool.Platform)
	if err != nil {
		return payload, 0, err
	}
	exportPlatform := platform
	if exportPlatform == model.AccountPoolPlatformXAI {
		exportPlatform = "grok"
	}

	var accounts []model.AccountPoolAccount
	if err := model.DB.Where("pool_id = ? AND status <> ?", poolID, model.AccountPoolAccountStatusDeleted).
		Order("id asc").Find(&accounts).Error; err != nil {
		return payload, 0, err
	}

	// Resolve the distinct proxies referenced by these accounts into stable proxy keys.
	// A corrupt proxy is skipped (logged) rather than failing the whole export.
	proxyKeyByID, proxies, err := s.exportAccountPoolProxies(accounts, includeSecrets)
	if err != nil {
		return payload, 0, err
	}
	payload.Proxies = proxies

	skipped := 0
	for _, account := range accounts {
		exported, err := exportAccountPoolAccount(account, exportPlatform, proxyKeyByID, includeSecrets)
		if err != nil {
			// Skip-and-continue: one undecryptable account must not lock the admin out
			// of exporting (backing up) every other account in the pool.
			common.SysError(fmt.Sprintf("account pool export: skipping account id=%d: %v", account.Id, err))
			skipped++
			continue
		}
		payload.Accounts = append(payload.Accounts, exported)
	}

	return payload, skipped, nil
}

// exportAccountPoolProxies loads the distinct proxies referenced by the given accounts and
// builds a stable proxy_key for each. The key is the proxy Name when it is unique and
// non-empty, otherwise "proxy-<id>". Returns a map from proxy id to its emitted key and the
// list of exported proxy entries (in ascending proxy-id order for determinism).
func (s AccountPoolService) exportAccountPoolProxies(accounts []model.AccountPoolAccount, includeSecrets bool) (map[int]string, []accountPoolSub2APIProxy, error) {
	referenced := make(map[int]struct{})
	orderedIDs := make([]int, 0)
	for _, account := range accounts {
		if account.ProxyID <= 0 {
			continue
		}
		if _, ok := referenced[account.ProxyID]; ok {
			continue
		}
		referenced[account.ProxyID] = struct{}{}
		orderedIDs = append(orderedIDs, account.ProxyID)
	}
	if len(orderedIDs) == 0 {
		return map[int]string{}, []accountPoolSub2APIProxy{}, nil
	}

	var proxies []model.AccountPoolProxy
	if err := model.DB.Where("id IN ? AND status <> ?", orderedIDs, model.AccountPoolProxyStatusDeleted).
		Order("id asc").Find(&proxies).Error; err != nil {
		return nil, nil, err
	}

	// Determine which names are unique so we only use a name as the stable key when it is
	// unambiguous; otherwise fall back to "proxy-<id>".
	nameCounts := make(map[string]int)
	for _, proxy := range proxies {
		name := strings.TrimSpace(proxy.Name)
		if name != "" {
			nameCounts[name]++
		}
	}

	keyByID := make(map[int]string, len(proxies))
	exported := make([]accountPoolSub2APIProxy, 0, len(proxies))
	for _, proxy := range proxies {
		name := strings.TrimSpace(proxy.Name)
		key := fmt.Sprintf("proxy-%d", proxy.Id)
		if name != "" && nameCounts[name] == 1 {
			key = name
		}

		auth, err := DecryptAccountPoolProxyAuthConfig(proxy.Password)
		if err != nil {
			// Skip a corrupt proxy rather than failing the whole export; accounts that
			// referenced it simply export without a proxy link.
			common.SysError(fmt.Sprintf("account pool export: skipping proxy id=%d: %v", proxy.Id, err))
			continue
		}

		// Prefer the decrypted username; fall back to the plaintext column.
		username := strings.TrimSpace(proxy.Username)
		if decrypted := strings.TrimSpace(auth.Username); decrypted != "" {
			username = decrypted
		}

		entry := accountPoolSub2APIProxy{
			ProxyKey: key,
			Name:     proxy.Name,
			Protocol: proxy.Protocol,
			Host:     proxy.Host,
			Port:     proxy.Port,
			Username: username,
			Status:   proxy.Status,
		}
		if password := strings.TrimSpace(auth.Password); password != "" {
			if includeSecrets {
				entry.Password = password
			} else {
				entry.Password = MaskAccountPoolSecretValue(password)
			}
		}
		keyByID[proxy.Id] = key
		exported = append(exported, entry)
	}

	return keyByID, exported, nil
}

// exportAccountPoolAccount converts a stored account into the sub2api export shape. The
// account's decrypted credential supplies the type/email/secrets, the token_state supplies
// the OAuth tokens + project_id, and the OAuthType column supplies oauth_type.
func exportAccountPoolAccount(account model.AccountPoolAccount, platform string, proxyKeyByID map[int]string, includeSecrets bool) (accountPoolSub2APIAccount, error) {
	credential, err := DecryptAccountPoolCredentialConfig(account.CredentialConfig)
	if err != nil {
		return accountPoolSub2APIAccount{}, fmt.Errorf("decrypt credential (id=%d): %w", account.Id, err)
	}
	tokenState, err := DecryptAccountPoolTokenState(account.TokenState)
	if err != nil {
		return accountPoolSub2APIAccount{}, fmt.Errorf("decrypt token state (id=%d): %w", account.Id, err)
	}

	credentialType := strings.TrimSpace(credential.Type)
	exported := accountPoolSub2APIAccount{
		Name:        account.Name,
		Platform:    platform,
		Type:        credentialType,
		Priority:    account.Priority,
		Credentials: map[string]any{},
	}

	concurrency := account.MaxConcurrency
	exported.Concurrency = &concurrency
	if account.ExpiresAt > 0 {
		expiresAt := account.ExpiresAt
		exported.ExpiresAt = &expiresAt
	}
	if account.AutoPauseOnExpired {
		autoPause := true
		exported.AutoPauseOnExpired = &autoPause
	}
	if proxyKey, ok := proxyKeyByID[account.ProxyID]; ok && account.ProxyID > 0 {
		key := proxyKey
		exported.ProxyKey = &key
	}

	// Always-emitted non-secret fields.
	if credentialType != "" {
		exported.Credentials["type"] = credentialType
	}
	if email := strings.TrimSpace(credential.Email); email != "" {
		exported.Credentials["email"] = email
	}
	if identifier := strings.TrimSpace(account.AccountIdentifier); identifier != "" {
		exported.Credentials["account_id"] = identifier
	}
	if oauthType := strings.TrimSpace(account.OAuthType); oauthType != "" {
		exported.Credentials["oauth_type"] = oauthType
	}
	if projectID := strings.TrimSpace(tokenState.ProjectID); projectID != "" {
		exported.Credentials["project_id"] = projectID
	}
	for key, value := range map[string]string{
		"client_id":          credential.ClientID,
		"scope":              credential.Scope,
		"token_type":         credential.TokenType,
		"sub":                credential.Subject,
		"team_id":            credential.TeamID,
		"subscription_tier":  credential.SubscriptionTier,
		"entitlement_status": credential.EntitlementStatus,
	} {
		if value = strings.TrimSpace(value); value != "" {
			exported.Credentials[key] = value
		}
	}

	// supported_models / model_mapping go under "extra" because the importer reads them
	// from Extra first (accountPoolSub2APIAccountCandidate prefers account.Extra).
	supportedModels, modelMapping, err := exportAccountPoolModelPolicy(account)
	if err != nil {
		return accountPoolSub2APIAccount{}, err
	}
	if len(supportedModels) > 0 || len(modelMapping) > 0 {
		extra := map[string]any{}
		if len(supportedModels) > 0 {
			extra["supported_models"] = supportedModels
		}
		if len(modelMapping) > 0 {
			extra["model_mapping"] = modelMapping
		}
		exported.Extra = extra
	}

	// Secret fields: real values only when explicitly requested, otherwise masked.
	apiKey := strings.TrimSpace(credential.APIKey)
	cfClearance := strings.TrimSpace(credential.CFClearance)
	refreshToken := strings.TrimSpace(credential.RefreshToken)
	idToken := strings.TrimSpace(credential.IDToken)
	accessToken := strings.TrimSpace(tokenState.AccessToken)
	if refreshToken == "" {
		refreshToken = strings.TrimSpace(tokenState.RefreshToken)
	}

	// emitSecret writes a secret credential field as its real value when secrets are
	// requested, otherwise as a masked placeholder. Empty values are skipped entirely.
	emitSecret := func(key, value string) {
		if value == "" {
			return
		}
		if includeSecrets {
			exported.Credentials[key] = value
		} else {
			exported.Credentials[key] = MaskAccountPoolSecretValue(value)
		}
	}

	if platform == model.AccountPoolPlatformGrokWeb {
		// grok.com web-cookie accounts store the sso token in APIKey, but the importer
		// reads it from "sso" (not "api_key") and the optional cf_clearance from
		// "cf_clearance". Emitting those names is what lets an include_secrets export
		// round-trip back through import; both are SECRET and masked when redacted.
		emitSecret("sso", apiKey)
		emitSecret("cf_clearance", cfClearance)
	} else {
		emitSecret("api_key", apiKey)
	}
	emitSecret("refresh_token", refreshToken)
	emitSecret("access_token", accessToken)
	emitSecret("id_token", idToken)
	if includeSecrets && tokenState.ExpiresAt > 0 {
		exported.Credentials["expires_at"] = tokenState.ExpiresAt
	}
	// expires_at is non-secret metadata, but a redacted export is inspection-only and
	// must not silently round-trip a usable masked OAuth credential, so it is omitted
	// alongside the masked tokens.

	return exported, nil
}

func exportAccountPoolModelPolicy(account model.AccountPoolAccount) ([]string, map[string]string, error) {
	var supportedModels []string
	if strings.TrimSpace(account.SupportedModels) != "" {
		if err := common.UnmarshalJsonStr(account.SupportedModels, &supportedModels); err != nil {
			return nil, nil, fmt.Errorf("decode supported_models (id=%d): %w", account.Id, err)
		}
	}
	var modelMapping map[string]string
	if strings.TrimSpace(account.ModelMapping) != "" {
		if err := common.UnmarshalJsonStr(account.ModelMapping, &modelMapping); err != nil {
			return nil, nil, fmt.Errorf("decode model_mapping (id=%d): %w", account.Id, err)
		}
	}
	return supportedModels, modelMapping, nil
}
