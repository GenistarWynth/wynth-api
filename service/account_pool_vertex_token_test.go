package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setAccountPoolVertexSATokenMintForTest(t *testing.T, mint accountPoolVertexSATokenMintFunc) {
	t.Helper()
	old := accountPoolVertexSATokenMint
	accountPoolVertexSATokenMint = mint
	t.Cleanup(func() { accountPoolVertexSATokenMint = old })
}

func TestAccountPoolServiceAccountMintsAndCachesToken(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	service := AccountPoolService{}
	pool := createAccountPoolServiceTestPool(t, service)
	credential := AccountPoolCredentialConfig{
		Type:               AccountPoolCredentialTypeServiceAccount,
		ServiceAccountJSON: `{"client_email":"svc@p.iam","project_id":"p","private_key":"x"}`,
		Location:           "us-central1",
	}
	account := createAccountPoolSchedulerAccount(t, service, pool.Id, AccountPoolAccountCreateParams{
		Name:       "vertex-sa",
		Credential: credential,
	})

	var mintCalls int32
	setAccountPoolVertexSATokenMintForTest(t, func(ctx context.Context, saJSON []byte, proxyURL string) (*CodexOAuthTokenResult, error) {
		atomic.AddInt32(&mintCalls, 1)
		assert.Equal(t, credential.ServiceAccountJSON, string(saJSON))
		return &CodexOAuthTokenResult{
			AccessToken: "ya29.minted",
			ExpiresAt:   time.Unix(5000, 0),
		}, nil
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID:  account.Id,
		Credential: credential,
		Now:        1000,
	})
	require.NoError(t, err)
	assert.Equal(t, "ya29.minted", token)
	assert.Equal(t, int32(1), atomic.LoadInt32(&mintCalls))

	// The minted token must be persisted (encrypted) into token_state.
	var stored model.AccountPoolAccount
	require.NoError(t, model.DB.First(&stored, account.Id).Error)
	reloaded, err := DecryptAccountPoolTokenState(stored.TokenState)
	require.NoError(t, err)
	assert.Equal(t, "ya29.minted", reloaded.AccessToken)
	assert.Equal(t, int64(5000), reloaded.ExpiresAt)
}

func TestAccountPoolServiceAccountReusesCachedToken(t *testing.T) {
	setupAccountPoolServiceTestDB(t)
	setAccountPoolVertexSATokenMintForTest(t, func(context.Context, []byte, string) (*CodexOAuthTokenResult, error) {
		t.Fatal("mint must not be called when a valid cached token exists")
		return nil, nil
	})

	token, err := ResolveAccountPoolRuntimeCredential(context.Background(), AccountPoolRuntimeCredentialRequest{
		AccountID: 1,
		Credential: AccountPoolCredentialConfig{
			Type:               AccountPoolCredentialTypeServiceAccount,
			ServiceAccountJSON: `{"client_email":"svc@p.iam","project_id":"p","private_key":"x"}`,
		},
		TokenState: AccountPoolTokenState{
			AccessToken: "ya29.cached",
			ExpiresAt:   9000,
			Version:     2,
		},
		Now: 1000,
	})
	require.NoError(t, err)
	assert.Equal(t, "ya29.cached", token)
}
