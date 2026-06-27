package service

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

const (
	AccountPoolCredentialTypeAPIKey = "api_key"
	AccountPoolCredentialTypeOAuth  = "oauth"
	// AccountPoolCredentialTypeServiceAccount identifies a GCP service-account
	// credential used for Gemini Vertex AI. The raw service-account JSON is stored
	// in the encrypted credential blob (ServiceAccountJSON) and a short-lived
	// access token is minted via the standard SA JWT-bearer flow at runtime.
	AccountPoolCredentialTypeServiceAccount = "service_account"

	// AccountPoolVertexDefaultLocation is the default Vertex AI region used when a
	// service-account credential does not specify one.
	AccountPoolVertexDefaultLocation = "us-central1"

	// AccountPoolGeminiOAuthTypeCodeAssist identifies Google Code Assist (Gemini CLI
	// code_assist scope) accounts, which additionally require a GCP project id.
	AccountPoolGeminiOAuthTypeCodeAssist = "code_assist"
	// AccountPoolGeminiOAuthTypeAIStudio identifies standard Google AI Studio OAuth accounts.
	AccountPoolGeminiOAuthTypeAIStudio = "ai_studio"
	// AccountPoolGeminiOAuthTypeAntigravity identifies Google Antigravity OAuth accounts.
	// Antigravity is a cloudcode-pa (v1internal) variant of Code Assist: it shares the
	// same endpoint, project detection and {project,model,request} wrapper, but uses its
	// own public OAuth client and adds requestType/userAgent/requestId fields to the wrapper.
	AccountPoolGeminiOAuthTypeAntigravity = "antigravity"
	// AccountPoolGeminiOAuthTypeGoogleOne identifies Google One AI OAuth accounts.
	// Per the sub2api reference, google_one uses the SAME built-in Gemini CLI OAuth client
	// as code_assist and routes through cloudcode-pa with project detection (no antigravity
	// client, no extra wrapper fields) — it is treated as a code_assist routing variant.
	AccountPoolGeminiOAuthTypeGoogleOne = "google_one"
)

type AccountPoolCredentialConfig struct {
	Type         string `json:"type"`
	APIKey       string `json:"api_key"`
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
	// ServiceAccountJSON holds the raw GCP service-account JSON for a Vertex AI
	// service_account credential. It is a SECRET and lives in the encrypted
	// credential blob. project_id is read from this JSON at runtime.
	ServiceAccountJSON string `json:"service_account_json,omitempty"`
	// Location is the Vertex AI region (e.g. us-central1) for a service_account
	// credential. Defaults to AccountPoolVertexDefaultLocation when empty.
	Location string `json:"location,omitempty"`
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

// NormalizeAccountPoolOAuthType trims and lowercases the plaintext oauth_type value.
// An empty/whitespace value normalizes to "" (no OAuth sub-type selected).
func NormalizeAccountPoolOAuthType(oauthType string) string {
	return strings.ToLower(strings.TrimSpace(oauthType))
}
