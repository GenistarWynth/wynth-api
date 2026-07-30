package service

import (
	"crypto/hmac"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"gorm.io/gorm"
)

const accountPoolCredentialsExportResourcePrefix = SecurityProofScopeAccountPoolCredentialsExport + ":pool:"

var (
	ErrProofResource = errors.New("security proof resource mismatch")
	ErrProofConsumed = errors.New("security proof has already been consumed")
	ErrProofStore    = errors.New("security proof replay store is unavailable")
)

func AccountPoolCredentialsExportResourceScope(poolID int) string {
	return accountPoolCredentialsExportResourcePrefix + strconv.Itoa(poolID)
}

func registerAccountPoolCredentialsExportProof(identity AuthIdentity, method string, scopes []string, expiresAt time.Time) (string, string, error) {
	hasExportScope := securityProofHasScope(scopes, SecurityProofScopeAccountPoolCredentialsExport)
	resource := ""
	for _, scope := range scopes {
		if !strings.HasPrefix(scope, accountPoolCredentialsExportResourcePrefix) {
			continue
		}
		if resource != "" {
			return "", "", ErrProofResource
		}
		resource = strings.TrimPrefix(scope, accountPoolCredentialsExportResourcePrefix)
	}
	if !hasExportScope && resource == "" {
		return "", "", nil
	}
	if !hasExportScope {
		return "", "", ErrProofScope
	}
	if method != "passkey" {
		return "", "", ErrProofMethod
	}
	if resource == "" {
		return "", "", ErrProofResource
	}
	poolID, err := strconv.Atoi(resource)
	if err != nil || poolID <= 0 || resource != strconv.Itoa(poolID) {
		return "", "", ErrProofResource
	}
	if model.DB == nil {
		common.SysError("account-pool export proof replay store is not configured")
		return "", "", ErrProofStore
	}
	replayToken, _, err := model.CreateAuthFlow(model.AuthFlowCreate{
		Purpose:   SecurityProofScopeAccountPoolCredentialsExport,
		UserId:    identity.UserID,
		SessionId: identity.SessionID,
		Payload:   resource,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		common.SysError("account-pool export proof replay store registration failed: " + err.Error())
		return "", "", ErrProofStore
	}
	return resource, replayToken, nil
}

func ConsumeAccountPoolCredentialsExportProof(raw string, identity AuthIdentity, poolID int) error {
	if poolID <= 0 {
		return ErrProofResource
	}
	claims, err := verifySecurityProofClaims(
		raw,
		identity,
		SecurityProofScopeAccountPoolCredentialsExport,
		[]string{"passkey"},
	)
	if err != nil {
		return err
	}
	requiredResource := strconv.Itoa(poolID)
	if !hmac.Equal([]byte(claims.Resource), []byte(requiredResource)) ||
		!securityProofHasScope(claims.Scopes, AccountPoolCredentialsExportResourceScope(poolID)) {
		return ErrProofResource
	}
	if model.DB == nil {
		common.SysError("account-pool export proof replay store is not configured")
		return ErrProofStore
	}

	_, err = model.ConsumeAuthFlowWithAction(
		claims.ID,
		model.AuthFlowMatch{
			Purpose:   SecurityProofScopeAccountPoolCredentialsExport,
			UserId:    identity.UserID,
			SessionId: identity.SessionID,
		},
		func(_ *gorm.DB, flow *model.AuthFlow) error {
			if !hmac.Equal([]byte(flow.Payload), []byte(requiredResource)) {
				return ErrProofResource
			}
			return nil
		},
	)
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrProofResource):
		return ErrProofResource
	case errors.Is(err, model.ErrAuthFlowConsumed):
		return ErrProofConsumed
	case errors.Is(err, model.ErrAuthFlowExpired):
		return ErrAuthTokenExpired
	case errors.Is(err, model.ErrAuthFlowInvalid):
		return ErrAuthTokenInvalid
	default:
		common.SysError("account-pool export proof replay store consumption failed: " + err.Error())
		return ErrProofStore
	}
}

func securityProofHasScope(scopes []string, required string) bool {
	for _, scope := range scopes {
		if hmac.Equal([]byte(scope), []byte(required)) {
			return true
		}
	}
	return false
}
