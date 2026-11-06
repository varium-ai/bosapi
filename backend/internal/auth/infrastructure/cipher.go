package infrastructure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// Cipher provides AES-GCM authenticated encryption for sensitive credentials like TOTP secrets.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher creates AES-GCM cipher from a 32-byte encryption key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("encryption key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt returns nonce-prefixed, base64-encoded ciphertext.
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte("bosapi/totp/v1"))
	return base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt authenticates and decrypts the ciphertext.
func (c *Cipher) Decrypt(encoded string) (string, error) {
	sealed, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(sealed) < c.aead.NonceSize() {
		return "", errors.New("invalid ciphertext")
	}
	nonce, payload := sealed[:c.aead.NonceSize()], sealed[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, payload, []byte("bosapi/totp/v1"))
	if err != nil {
		return "", errors.New("failed to decrypt ciphertext")
	}
	return string(plaintext), nil
}
