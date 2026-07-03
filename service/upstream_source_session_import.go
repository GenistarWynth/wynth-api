package service

import (
	"context"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// ApplyUpstreamSourceImportedSession validates an admin-imported session with a
// live probe (DiscoverGroups) and, on success, persists it (encrypted) so
// subsequent discover/sync short-circuit login.
func ApplyUpstreamSourceImportedSession(ctx context.Context, source *model.UpstreamSource, req dto.UpstreamSourceSessionImportRequest) error {
	if source == nil {
		return errors.New("upstream source is required")
	}

	finalJSON, err := buildImportedAuthConfigJSON(source, req) // imported session + preserved email/password
	if err != nil {
		return err
	}
	probeJSON, err := stripCredentialsFromAuthConfig(source.Type, finalJSON) // imported session ONLY (no email/password)
	if err != nil {
		return err
	}
	source.AuthConfig = probeJSON // in-memory plaintext; probe reads via ReadUpstreamSourceAuthConfig

	adapter, err := DefaultUpstreamSourceAdapterFactory(source.Type)
	if err != nil {
		return err
	}
	if _, err := adapter.DiscoverGroups(ctx, source); err != nil {
		return errors.New("imported session failed validation: " + SanitizeUpstreamSourceError(err))
	}

	stored, err := WriteUpstreamSourceAuthConfig(finalJSON)
	if err != nil {
		return err
	}
	source.AuthConfig = finalJSON
	if err := model.PersistUpstreamSourceAuthConfig(source.Id, stored); err != nil {
		return err
	}
	// A validated import proves the block is resolved; clear the sentinel so
	// turnstile_blocked flips to false in the response confirming the import.
	return model.ClearUpstreamSourceTurnstileBlock(source.Id, ErrUpstreamSourceTurnstileRequired.Error())
}

// stripCredentialsFromAuthConfig removes stored email/password from an
// already-built auth config JSON so the live validation probe cannot trigger
// new-api's fallback password re-login (newAPIManagementRequest only retries
// a 401 with loginManagementAuth when newAPIAuthConfigHasCredentials is
// true). This ensures the probe validates the admin's SPECIFIC pasted
// session rather than silently succeeding via stored credentials.
func stripCredentialsFromAuthConfig(sourceType string, plaintext string) (string, error) {
	switch sourceType {
	case model.UpstreamSourceTypeNewAPI:
		var cfg newAPIAuthConfig
		if err := common.UnmarshalJsonStr(plaintext, &cfg); err != nil {
			return "", err
		}
		cfg.Email = ""
		cfg.Password = ""
		data, err := common.Marshal(cfg)
		return string(data), err
	case model.UpstreamSourceTypeSub2API:
		var cfg sub2APIAuthConfig
		if err := common.UnmarshalJsonStr(plaintext, &cfg); err != nil {
			return "", err
		}
		cfg.Email = ""
		cfg.Password = ""
		data, err := common.Marshal(cfg)
		return string(data), err
	default:
		return "", errors.New("unsupported upstream source type for session import")
	}
}

func buildImportedAuthConfigJSON(source *model.UpstreamSource, req dto.UpstreamSourceSessionImportRequest) (string, error) {
	// Preserve existing email/password so credential rotation is not required.
	existing, err := ReadUpstreamSourceAuthConfig(source.AuthConfig)
	if err != nil {
		return "", err
	}

	switch source.Type {
	case model.UpstreamSourceTypeNewAPI:
		var cfg newAPIAuthConfig
		if strings.TrimSpace(existing) != "" {
			if err := common.UnmarshalJsonStr(existing, &cfg); err != nil {
				return "", err
			}
		}
		if token, uid, ok := deriveNewAPISessionFromImport(source, req); ok {
			cfg.AccessToken = token
			cfg.UserID = uid
			cfg.SessionSource = "manual"
		} else {
			return "", errors.New("provide either an access token + user id, or a session cookie for new-api")
		}
		data, err := common.Marshal(cfg)
		return string(data), err
	case model.UpstreamSourceTypeSub2API:
		var cfg sub2APIAuthConfig
		if strings.TrimSpace(existing) != "" {
			if err := common.UnmarshalJsonStr(existing, &cfg); err != nil {
				return "", err
			}
		}
		if strings.TrimSpace(req.AccessToken) == "" {
			return "", errors.New("sub2api session import requires an access token")
		}
		cfg.AccessToken = strings.TrimSpace(req.AccessToken)
		if req.RefreshToken != "" {
			cfg.RefreshToken = req.RefreshToken
		}
		cfg.ExpiresAt = req.ExpiresAt
		if cfg.ExpiresAt == 0 {
			cfg.ExpiresAt = common.GetTimestamp() + 3600
		}
		cfg.SessionSource = "manual"
		data, err := common.Marshal(cfg)
		return string(data), err
	default:
		return "", errors.New("unsupported upstream source type for session import")
	}
}

// deriveNewAPISessionFromImport returns access_token + user_id from either the
// direct token+id fields or by replaying a pasted session cookie against
// /user/token and /user/self.
func deriveNewAPISessionFromImport(source *model.UpstreamSource, req dto.UpstreamSourceSessionImportRequest) (string, int, bool) {
	if strings.TrimSpace(req.AccessToken) != "" && req.UserID > 0 {
		return strings.TrimSpace(req.AccessToken), req.UserID, true
	}
	if strings.TrimSpace(req.SessionCookie) != "" {
		token, uid, err := newAPIExchangeCookieForToken(source, req.SessionCookie)
		if err == nil && token != "" && uid > 0 {
			return token, uid, true
		}
	}
	return "", 0, false
}
