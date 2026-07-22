package service

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"gorm.io/gorm"
)

const upstreamSourceAuthExpiringWithinSeconds int64 = 5 * 60

// loadUpstreamSourceRuntimeAuth overlays a dedicated session row onto the
// source-owned credentials for unchanged adapters. When no session row exists,
// the legacy mixed AuthConfig remains readable as-is.
func loadUpstreamSourceRuntimeAuth(source *model.UpstreamSource) (*model.UpstreamSourceSession, error) {
	if source == nil {
		return nil, errors.New("upstream source is required")
	}
	if source.Id == 0 {
		return nil, nil
	}
	session, err := model.GetUpstreamSourceSession(source.Id)
	if err != nil || session == nil || strings.TrimSpace(session.SessionConfig) == "" {
		return session, err
	}
	credentials, err := ReadUpstreamSourceAuthConfig(source.AuthConfig)
	if err != nil {
		return nil, err
	}
	sessionConfig, err := ReadUpstreamSourceAuthConfig(session.SessionConfig)
	if err != nil {
		return nil, err
	}
	combined, err := mergeUpstreamSourceCredentials(source.Type, sessionConfig, credentials)
	if err != nil {
		return nil, err
	}
	source.AuthConfig = combined
	return session, nil
}

func GetUpstreamSourceAuthHealth(source *model.UpstreamSource, now int64) (model.UpstreamSourceSession, error) {
	if source == nil || source.Id == 0 {
		return model.UpstreamSourceSession{}, errors.New("persisted upstream source is required")
	}
	session, err := model.GetUpstreamSourceSession(source.Id)
	if err != nil {
		return model.UpstreamSourceSession{}, err
	}
	if session == nil {
		health := model.UpstreamSourceSession{SourceID: source.Id, AuthStatus: model.UpstreamSourceAuthStatusUnknown}
		plaintext, readErr := ReadUpstreamSourceAuthConfig(source.AuthConfig)
		if readErr != nil {
			return health, readErr
		}
		_, _, health.SessionSource, health.ExpiresAt, _ = splitUpstreamSourceAuthConfig(source.Type, plaintext)
		return health, nil
	}

	changed := false
	if session.AuthStatus == model.UpstreamSourceAuthStatusHealthy || session.AuthStatus == model.UpstreamSourceAuthStatusExpiring {
		nextStatus := model.UpstreamSourceAuthStatusHealthy
		if session.ExpiresAt > 0 && session.ExpiresAt <= now {
			nextStatus = model.UpstreamSourceAuthStatusExpired
		} else if session.ExpiresAt > 0 && session.ExpiresAt <= now+upstreamSourceAuthExpiringWithinSeconds {
			nextStatus = model.UpstreamSourceAuthStatusExpiring
		}
		if session.AuthStatus != nextStatus {
			session.AuthStatus = nextStatus
			changed = true
		}
	}
	sanitized := SanitizeUpstreamSourceError(errors.New(session.LastAuthError))
	if session.LastAuthError != sanitized {
		session.LastAuthError = sanitized
		changed = true
	}
	if changed {
		session.UpdatedTime = now
		if err := model.UpsertUpstreamSourceSessionTx(model.DB, session); err != nil {
			return model.UpstreamSourceSession{}, err
		}
	}
	return *session, nil
}

func recordUpstreamSourceAuthFailure(source *model.UpstreamSource, authErr error, now int64) {
	status, authFailure := classifyUpstreamSourceAuthError(authErr)
	if source == nil || source.Id == 0 || !authFailure {
		return
	}
	existing, err := model.GetUpstreamSourceSession(source.Id)
	if err != nil {
		common.SysError("failed to load upstream source auth health: " + SanitizeUpstreamSourceError(err))
		return
	}
	session := model.UpstreamSourceSession{
		SourceID:      source.Id,
		AuthStatus:    status,
		LastAuthError: SanitizeUpstreamSourceError(authErr),
		CreatedTime:   now,
		UpdatedTime:   now,
	}
	if existing != nil {
		session = *existing
		session.AuthStatus = status
		session.LastAuthError = SanitizeUpstreamSourceError(authErr)
		session.UpdatedTime = now
	} else if plaintext, readErr := ReadUpstreamSourceAuthConfig(source.AuthConfig); readErr == nil {
		_, _, session.SessionSource, session.ExpiresAt, _ = splitUpstreamSourceAuthConfig(source.Type, plaintext)
	}
	if err := model.UpsertUpstreamSourceSessionTx(model.DB, &session); err != nil {
		common.SysError("failed to persist upstream source auth health: " + SanitizeUpstreamSourceError(err))
	}
}

func classifyUpstreamSourceAuthError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var requestErr newAPIRequestError
	if errors.As(err, &requestErr) && isNewAPIAuthError(requestErr) {
		return model.UpstreamSourceAuthStatusExpired, true
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "status 401") ||
		strings.Contains(text, "invalid access token") || strings.Contains(text, "invalid access_token") ||
		strings.Contains(text, "token expired") || strings.Contains(text, "expired token") ||
		strings.Contains(text, "not logged in") || strings.Contains(text, "unauthorized") ||
		strings.Contains(text, "未登录") || strings.Contains(text, "访问令牌") {
		return model.UpstreamSourceAuthStatusExpired, true
	}
	if errors.Is(err, ErrUpstreamSourceTurnstileRequired) || errors.Is(err, ErrUpstreamSource2FARequired) ||
		strings.Contains(text, "credentials are required") || strings.Contains(text, "email and password are required") ||
		strings.Contains(text, "username/email and password are required") {
		return model.UpstreamSourceAuthStatusFailed, true
	}
	return "", false
}

func persistUpstreamSourceAuthSession(source *model.UpstreamSource, plaintext string, now int64, refreshed bool) error {
	if source == nil || source.Id == 0 {
		return errors.New("persisted upstream source is required")
	}
	credentialsConfig, sessionConfig, sessionSource, expiresAt, err := splitUpstreamSourceAuthConfig(source.Type, plaintext)
	if err != nil {
		return err
	}
	storedCredentials, err := WriteUpstreamSourceAuthConfig(credentialsConfig)
	if err != nil {
		return err
	}
	storedSession, err := WriteUpstreamSourceAuthConfig(sessionConfig)
	if err != nil {
		return err
	}

	existing, err := model.GetUpstreamSourceSession(source.Id)
	if err != nil {
		return err
	}
	lastRefreshedAt := int64(0)
	createdTime := now
	if existing != nil {
		lastRefreshedAt = existing.LastRefreshedAt
		createdTime = existing.CreatedTime
	}
	if refreshed {
		lastRefreshedAt = now
	}
	session := model.UpstreamSourceSession{
		SourceID:        source.Id,
		SessionConfig:   storedSession,
		SessionSource:   sessionSource,
		AuthStatus:      model.UpstreamSourceAuthStatusHealthy,
		LastValidatedAt: now,
		LastRefreshedAt: lastRefreshedAt,
		ExpiresAt:       expiresAt,
		LastAuthError:   "",
		CreatedTime:     createdTime,
		UpdatedTime:     now,
	}
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UpstreamSource{}).Where("id = ?", source.Id).Updates(map[string]interface{}{
			"auth_config":  storedCredentials,
			"updated_time": now,
		}).Error; err != nil {
			return err
		}
		return model.UpsertUpstreamSourceSessionTx(tx, &session)
	}); err != nil {
		return err
	}
	source.AuthConfig = plaintext
	return nil
}

// ClearUpstreamSourceSession removes only replaceable login material. It also
// strips legacy cached tokens from AuthConfig so the compatibility fallback
// cannot resurrect a session after the dedicated row is deleted.
func ClearUpstreamSourceSession(sourceID int) error {
	if sourceID == 0 {
		return errors.New("source ID is required")
	}
	var source model.UpstreamSource
	if err := model.DB.First(&source, sourceID).Error; err != nil {
		return err
	}
	plaintext, err := ReadUpstreamSourceAuthConfig(source.AuthConfig)
	if err != nil {
		return err
	}
	credentialsConfig, _, _, _, err := splitUpstreamSourceAuthConfig(source.Type, plaintext)
	if err != nil {
		return err
	}
	storedCredentials, err := WriteUpstreamSourceAuthConfig(credentialsConfig)
	if err != nil {
		return err
	}
	now := common.GetTimestamp()
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.UpstreamSource{}).Where("id = ?", sourceID).Updates(map[string]interface{}{
			"auth_config":  storedCredentials,
			"updated_time": now,
		}).Error; err != nil {
			return err
		}
		return model.ClearUpstreamSourceSessionTx(tx, sourceID)
	})
}

func upstreamSourceSessionChanged(sourceType string, before string, after string) bool {
	beforePlaintext, err := ReadUpstreamSourceAuthConfig(before)
	if err != nil {
		return true
	}
	afterPlaintext, err := ReadUpstreamSourceAuthConfig(after)
	if err != nil {
		return true
	}
	_, beforeSession, _, _, err := splitUpstreamSourceAuthConfig(sourceType, beforePlaintext)
	if err != nil {
		return true
	}
	_, afterSession, _, _, err := splitUpstreamSourceAuthConfig(sourceType, afterPlaintext)
	if err != nil {
		return true
	}
	return beforeSession != afterSession
}

func persistUpstreamSourceAuthState(source *model.UpstreamSource, before string, now int64, validated bool) {
	if source == nil {
		return
	}
	refreshed := upstreamSourceSessionChanged(source.Type, before, source.AuthConfig)
	if !validated && !refreshed {
		return
	}
	if err := persistUpstreamSourceAuthSession(source, source.AuthConfig, now, refreshed); err != nil {
		common.SysError("failed to persist upstream source session state: " + SanitizeUpstreamSourceError(err))
	}
}

func splitUpstreamSourceAuthConfig(sourceType string, plaintext string) (credentials string, session string, sessionSource string, expiresAt int64, err error) {
	switch strings.TrimSpace(sourceType) {
	case model.UpstreamSourceTypeNewAPI:
		var combined newAPIAuthConfig
		if strings.TrimSpace(plaintext) != "" {
			if err := common.UnmarshalJsonStr(plaintext, &combined); err != nil {
				return "", "", "", 0, err
			}
		}
		credentialsData, err := common.Marshal(struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}{Email: combined.Email, Password: combined.Password})
		if err != nil {
			return "", "", "", 0, err
		}
		if strings.TrimSpace(combined.AccessToken) == "" && combined.UserID == 0 && strings.TrimSpace(combined.SessionSource) == "" {
			return string(credentialsData), "", "", 0, nil
		}
		sessionData, err := common.Marshal(struct {
			AccessToken   string `json:"access_token"`
			UserID        int    `json:"user_id"`
			SessionSource string `json:"session_source,omitempty"`
		}{
			AccessToken:   combined.AccessToken,
			UserID:        combined.UserID,
			SessionSource: combined.SessionSource,
		})
		if err != nil {
			return "", "", "", 0, err
		}
		return string(credentialsData), string(sessionData), combined.SessionSource, 0, nil
	case model.UpstreamSourceTypeSub2API:
		var combined sub2APIAuthConfig
		if strings.TrimSpace(plaintext) != "" {
			if err := common.UnmarshalJsonStr(plaintext, &combined); err != nil {
				return "", "", "", 0, err
			}
		}
		credentialsData, err := common.Marshal(struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}{Email: combined.Email, Password: combined.Password})
		if err != nil {
			return "", "", "", 0, err
		}
		if strings.TrimSpace(combined.AccessToken) == "" && strings.TrimSpace(combined.RefreshToken) == "" {
			return string(credentialsData), "", "", 0, nil
		}
		sessionData, err := common.Marshal(struct {
			AccessToken   string `json:"access_token"`
			RefreshToken  string `json:"refresh_token"`
			ExpiresAt     int64  `json:"expires_at"`
			SessionSource string `json:"session_source,omitempty"`
		}{
			AccessToken:   combined.AccessToken,
			RefreshToken:  combined.RefreshToken,
			ExpiresAt:     combined.ExpiresAt,
			SessionSource: combined.SessionSource,
		})
		if err != nil {
			return "", "", "", 0, err
		}
		return string(credentialsData), string(sessionData), combined.SessionSource, combined.ExpiresAt, nil
	default:
		return "", "", "", 0, errors.New("unsupported upstream source type")
	}
}
