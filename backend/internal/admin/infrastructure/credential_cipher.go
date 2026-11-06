package infrastructure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// CredentialCipher encrypts provider credentials using authenticated encryption.
type CredentialCipher struct{ aead cipher.AEAD }

// NewCredentialCipher creates AES-GCM encryption from a 32-byte key.
func NewCredentialCipher(key []byte) (*CredentialCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("credential encryption key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &CredentialCipher{aead: aead}, nil
}

// Encrypt returns nonce-prefixed, base64-encoded ciphertext.
func (ciphertext *CredentialCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, ciphertext.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := ciphertext.aead.Seal(nonce, nonce, []byte(plaintext), []byte("bosapi/provider/v1"))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt authenticates and opens provider credentials encrypted by this cipher.
func (ciphertext *CredentialCipher) Decrypt(encoded string) (string, error) {
	sealed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < ciphertext.aead.NonceSize() {
		return "", errors.New("invalid provider credential ciphertext")
	}
	nonce, payload := sealed[:ciphertext.aead.NonceSize()], sealed[ciphertext.aead.NonceSize():]
	plaintext, err := ciphertext.aead.Open(nil, nonce, payload, []byte("bosapi/provider/v1"))
	if err != nil {
		return "", errors.New("invalid provider credential ciphertext")
	}
	return string(plaintext), nil
}
