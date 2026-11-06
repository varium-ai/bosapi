package infrastructure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// APIKeyCipher encrypts and decrypts sensitive raw API keys using AES-256-GCM.
type APIKeyCipher struct {
	aead cipher.AEAD
}

// NewAPIKeyCipher creates an AES-GCM cipher from a 32-byte master key.
func NewAPIKeyCipher(key []byte) (*APIKeyCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("api key encryption key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &APIKeyCipher{aead: aead}, nil
}

// Encrypt returns nonce-prefixed, base64-encoded ciphertext.
func (c *APIKeyCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte("bosapi/api_key/v1"))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt authenticates and opens ciphertext encrypted by this cipher.
func (c *APIKeyCipher) Decrypt(encoded string) (string, error) {
	sealed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < c.aead.NonceSize() {
		return "", errors.New("invalid api key ciphertext")
	}
	nonce, payload := sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, payload, []byte("bosapi/api_key/v1"))
	if err != nil {
		return "", errors.New("failed to decrypt api key ciphertext")
	}
	return string(plaintext), nil
}
