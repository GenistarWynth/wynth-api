package common

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const secretEnvelopeVersion = 1
const secretEnvelopeAlgorithm = "AES-256-GCM"

var ErrUnstableSecretEnvelopeKey = errors.New("account pool secret encryption requires CRYPTO_SECRET or SESSION_SECRET to be set")

type SecretEnvelope struct {
	Version    int    `json:"v"`
	Algorithm  string `json:"alg"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

func EncryptSecretString(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	block, err := newSecretEnvelopeBlock()
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	envelope := SecretEnvelope{
		Version:    secretEnvelopeVersion,
		Algorithm:  secretEnvelopeAlgorithm,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}

	data, err := Marshal(envelope)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

func DecryptSecretString(envelope string) (string, error) {
	if envelope == "" {
		return "", nil
	}

	var secretEnvelope SecretEnvelope
	if err := UnmarshalJsonStr(envelope, &secretEnvelope); err != nil {
		return "", err
	}

	if secretEnvelope.Version != secretEnvelopeVersion {
		return "", fmt.Errorf("unsupported secret envelope version %d", secretEnvelope.Version)
	}
	if secretEnvelope.Algorithm != secretEnvelopeAlgorithm {
		return "", fmt.Errorf("unsupported secret envelope algorithm %q", secretEnvelope.Algorithm)
	}

	nonce, err := base64.StdEncoding.DecodeString(secretEnvelope.Nonce)
	if err != nil {
		return "", err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(secretEnvelope.Ciphertext)
	if err != nil {
		return "", err
	}

	block, err := newSecretEnvelopeBlock()
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(nonce) != gcm.NonceSize() {
		return "", errors.New("invalid secret envelope nonce size")
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

func newSecretEnvelopeBlock() (cipher.Block, error) {
	if !CryptoSecretStable {
		return nil, ErrUnstableSecretEnvelopeKey
	}

	sum := sha256.Sum256([]byte("wynth-account-pool:" + CryptoSecret))
	return aes.NewCipher(sum[:])
}
