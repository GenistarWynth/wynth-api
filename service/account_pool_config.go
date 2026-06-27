package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	AccountPoolCredentialTypeAPIKey = "api_key"
	AccountPoolCredentialTypeOAuth  = "oauth"

	// AccountPoolGeminiOAuthTypeCodeAssist identifies Google Code Assist (Gemini CLI
	// code_assist scope) accounts, which additionally require a GCP project id.
	AccountPoolGeminiOAuthTypeCodeAssist = "code_assist"
	// AccountPoolGeminiOAuthTypeAIStudio identifies standard Google AI Studio OAuth accounts.
	AccountPoolGeminiOAuthTypeAIStudio = "ai_studio"
)

type AccountPoolCredentialConfig struct {
	Type         string `json:"type"`
	APIKey       string `json:"api_key"`
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
	// OAuthType narrows the OAuth flow within a given platform.
	// For Gemini, "code_assist" selects the Code Assist endpoint (cloudcode-pa.googleapis.com);
	// "ai_studio" selects standard AI Studio OAuth.
	// Omitempty keeps legacy encrypted blobs backward-compatible (decrypt → empty string).
	OAuthType string `json:"oauth_type,omitempty"`
}

type AccountPoolTokenState struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	Version      int64  `json:"version"`
	// ProjectID holds the GCP project id used for Gemini Code Assist accounts.
	// Omitempty keeps legacy encrypted blobs backward-compatible (decrypt → empty string).
	ProjectID string `json:"project_id,omitempty"`
}

type AccountPoolProxyAuthConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AccountPoolModelPolicy struct {
	Strategy    string   `json:"strategy"`
	FixedModels []string `json:"fixed_models"`
}

type AccountPoolAccountFilterConfig struct {
	AccountIDs []int `json:"account_ids"`
}

func (s AccountPoolTokenState) NextVersion() AccountPoolTokenState {
	s.Version++
	return s
}

func EncryptAccountPoolCredentialConfig(config AccountPoolCredentialConfig) (string, error) {
	if !accountPoolCredentialHasValue(config) {
		return "", nil
	}
	data, err := common.Marshal(config)
	if err != nil {
		return "", err
	}
	return common.EncryptSecretString(string(data))
}

func DecryptAccountPoolCredentialConfig(encrypted string) (AccountPoolCredentialConfig, error) {
	var config AccountPoolCredentialConfig
	if encrypted == "" {
		return config, nil
	}
	plaintext, err := common.DecryptSecretString(encrypted)
	if err != nil {
		return config, err
	}
	if err := common.UnmarshalJsonStr(plaintext, &config); err != nil {
		return config, err
	}
	return config, nil
}

func EncryptAccountPoolTokenState(state AccountPoolTokenState) (string, error) {
	if !accountPoolTokenStateHasSecret(state) {
		return "", nil
	}
	data, err := common.Marshal(state)
	if err != nil {
		return "", err
	}
	return common.EncryptSecretString(string(data))
}

func DecryptAccountPoolTokenState(encrypted string) (AccountPoolTokenState, error) {
	var state AccountPoolTokenState
	if encrypted == "" {
		return state, nil
	}
	plaintext, err := common.DecryptSecretString(encrypted)
	if err != nil {
		return state, err
	}
	if err := common.UnmarshalJsonStr(plaintext, &state); err != nil {
		return state, err
	}
	return state, nil
}

func EncryptAccountPoolProxyAuthConfig(config AccountPoolProxyAuthConfig) (string, error) {
	if strings.TrimSpace(config.Password) == "" {
		return "", nil
	}
	data, err := common.Marshal(config)
	if err != nil {
		return "", err
	}
	return common.EncryptSecretString(string(data))
}

func DecryptAccountPoolProxyAuthConfig(encrypted string) (AccountPoolProxyAuthConfig, error) {
	var config AccountPoolProxyAuthConfig
	if encrypted == "" {
		return config, nil
	}
	plaintext, err := common.DecryptSecretString(encrypted)
	if err != nil {
		return config, err
	}
	if err := common.UnmarshalJsonStr(plaintext, &config); err != nil {
		return config, err
	}
	return config, nil
}

func MaskAccountPoolSecretValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "***"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func accountPoolTokenStateHasSecret(state AccountPoolTokenState) bool {
	return strings.TrimSpace(state.AccessToken) != "" || strings.TrimSpace(state.RefreshToken) != ""
}
