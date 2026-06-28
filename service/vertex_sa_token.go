package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// vertexSAScope is the OAuth scope requested for Vertex AI access via a
// service-account JWT-bearer exchange.
const vertexSAScope = "https://www.googleapis.com/auth/cloud-platform"

// vertexSADefaultTokenURI is the Google OAuth2 token endpoint used as a fallback
// when the service-account JSON does not carry a token_uri.
const vertexSADefaultTokenURI = "https://oauth2.googleapis.com/token"

// vertexSAJWTBearerGrantType is the RFC 7523 JWT-bearer grant type.
const vertexSAJWTBearerGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"

// vertexSATokenURLOverride lets tests redirect the token exchange to an httptest
// server. When non-empty it takes precedence over the SA JSON token_uri.
var vertexSATokenURLOverride = ""

// vertexServiceAccount is the subset of the GCP service-account JSON we need to
// mint an access token.
type vertexServiceAccount struct {
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
	ProjectID    string `json:"project_id"`
	Type         string `json:"type"`
}

// vertexSAJWTHeader is the JOSE header for the signed assertion.
type vertexSAJWTHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid,omitempty"`
}

// vertexSAJWTClaims are the JWT-bearer assertion claims.
type vertexSAJWTClaims struct {
	Iss   string `json:"iss"`
	Scope string `json:"scope"`
	Aud   string `json:"aud"`
	Iat   int64  `json:"iat"`
	Exp   int64  `json:"exp"`
}

// VertexServiceAccountInfo carries the non-secret fields extracted from a GCP
// service-account JSON.
type VertexServiceAccountInfo struct {
	ProjectID   string
	TokenURI    string
	ClientEmail string
}

// ExtractVertexServiceAccountInfo parses a service-account JSON and returns its
// project_id, token_uri (falling back to the default endpoint) and client_email.
func ExtractVertexServiceAccountInfo(saJSON []byte) (VertexServiceAccountInfo, error) {
	sa, err := parseVertexServiceAccount(saJSON)
	if err != nil {
		return VertexServiceAccountInfo{}, err
	}
	tokenURI := strings.TrimSpace(sa.TokenURI)
	if tokenURI == "" {
		tokenURI = vertexSADefaultTokenURI
	}
	return VertexServiceAccountInfo{
		ProjectID:   strings.TrimSpace(sa.ProjectID),
		TokenURI:    tokenURI,
		ClientEmail: strings.TrimSpace(sa.ClientEmail),
	}, nil
}

func parseVertexServiceAccount(saJSON []byte) (vertexServiceAccount, error) {
	var sa vertexServiceAccount
	if len(strings.TrimSpace(string(saJSON))) == 0 {
		return sa, errors.New("vertex service account json is empty")
	}
	if err := common.Unmarshal(saJSON, &sa); err != nil {
		return sa, fmt.Errorf("parse vertex service account json: %w", err)
	}
	if strings.TrimSpace(sa.ClientEmail) == "" {
		return sa, errors.New("vertex service account json missing client_email")
	}
	if strings.TrimSpace(sa.PrivateKey) == "" {
		return sa, errors.New("vertex service account json missing private_key")
	}
	if strings.TrimSpace(sa.ProjectID) == "" {
		return sa, errors.New("vertex service account json missing project_id")
	}
	return sa, nil
}

// MintVertexServiceAccountToken performs the standard service-account
// JWT-bearer flow: it builds an RS256-signed JWT assertion from the SA JSON and
// exchanges it at the token endpoint for a short-lived access token.
//
// The exchange is routed through proxyURL when set. Secrets are masked in errors.
func MintVertexServiceAccountToken(ctx context.Context, saJSON []byte, proxyURL string) (*CodexOAuthTokenResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sa, err := parseVertexServiceAccount(saJSON)
	if err != nil {
		return nil, err
	}

	privateKey, err := parseVertexRSAPrivateKey(sa.PrivateKey)
	if err != nil {
		return nil, err
	}

	tokenURI := strings.TrimSpace(sa.TokenURI)
	if tokenURI == "" {
		tokenURI = vertexSADefaultTokenURI
	}

	assertion, err := buildVertexSAAssertion(sa, tokenURI, privateKey, time.Now())
	if err != nil {
		return nil, err
	}

	exchangeURL := tokenURI
	if strings.TrimSpace(vertexSATokenURLOverride) != "" {
		exchangeURL = vertexSATokenURLOverride
	}

	client, err := getGeminiOAuthHTTPClient(proxyURL)
	if err != nil {
		return nil, err
	}
	return exchangeVertexSAAssertion(ctx, client, exchangeURL, assertion)
}

// parseVertexRSAPrivateKey parses a PEM-encoded PKCS#8 or PKCS#1 RSA private key.
func parseVertexRSAPrivateKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("vertex service account private_key is not valid PEM")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("vertex service account private_key is not an RSA key")
		}
		return rsaKey, nil
	}
	rsaKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse vertex service account private_key: %w", err)
	}
	return rsaKey, nil
}

// buildVertexSAAssertion builds and RS256-signs the JWT-bearer assertion.
func buildVertexSAAssertion(sa vertexServiceAccount, audience string, key *rsa.PrivateKey, now time.Time) (string, error) {
	header := vertexSAJWTHeader{Alg: "RS256", Typ: "JWT", Kid: strings.TrimSpace(sa.PrivateKeyID)}
	claims := vertexSAJWTClaims{
		Iss:   strings.TrimSpace(sa.ClientEmail),
		Scope: vertexSAScope,
		Aud:   audience,
		Iat:   now.Unix(),
		Exp:   now.Add(time.Hour).Unix(),
	}

	headerJSON, err := common.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal vertex jwt header: %w", err)
	}
	claimsJSON, err := common.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal vertex jwt claims: %w", err)
	}

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(claimsJSON)

	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign vertex jwt: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func exchangeVertexSAAssertion(ctx context.Context, client *http.Client, tokenURL string, assertion string) (*CodexOAuthTokenResult, error) {
	form := url.Values{}
	form.Set("grant_type", vertexSAJWTBearerGrantType)
	form.Set("assertion", assertion)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, maskVertexSAError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("vertex service account token exchange failed: status=%d", resp.StatusCode)
	}

	var payload struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := common.DecodeJson(resp.Body, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.AccessToken) == "" {
		return nil, errors.New("vertex service account token exchange response missing access_token")
	}
	if payload.ExpiresIn <= 0 {
		return nil, errors.New("vertex service account token exchange response missing expires_in")
	}

	return &CodexOAuthTokenResult{
		AccessToken: strings.TrimSpace(payload.AccessToken),
		ExpiresAt:   time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second),
	}, nil
}

// maskVertexSAError masks any secret material that may have leaked into a
// transport error string (e.g. a token endpoint URL with embedded query data).
func maskVertexSAError(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(common.MaskSensitiveInfo(err.Error()))
}
