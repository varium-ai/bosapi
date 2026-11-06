package infrastructure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// EncryptedStore persists JSON aggregates using AES-GCM and restrictive file permissions.
type EncryptedStore struct {
	path string
	key  []byte
}

// NewEncryptedStore constructs an encrypted store for one gateway aggregate.
func NewEncryptedStore(path string, key []byte) *EncryptedStore {
	return &EncryptedStore{path: path, key: append([]byte(nil), key...)}
}

// Load decrypts the aggregate into target and treats a missing file as empty state.
func (store *EncryptedStore) Load(target any) error {
	raw, err := os.ReadFile(store.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read encrypted gateway data: %w", err)
	}
	gcm, err := store.cipher()
	if err != nil {
		return err
	}
	if len(raw) < gcm.NonceSize() {
		return errors.New("invalid encrypted gateway data")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return fmt.Errorf("decrypt gateway data: %w", err)
	}
	if err := json.Unmarshal(plain, target); err != nil {
		return fmt.Errorf("decode gateway data: %w", err)
	}
	return nil
}

// Save encrypts an aggregate and atomically replaces the local data file.
func (store *EncryptedStore) Save(value any) error {
	plain, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode gateway data: %w", err)
	}
	gcm, err := store.cipher()
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate encryption nonce: %w", err)
	}
	encrypted := gcm.Seal(nonce, nonce, plain, nil)
	if err := os.MkdirAll(filepath.Dir(store.path), 0700); err != nil {
		return fmt.Errorf("create gateway data directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(store.path), ".gateway-*.tmp")
	if err != nil {
		return fmt.Errorf("create gateway data file: %w", err)
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set gateway data permissions: %w", err)
	}
	if _, err := temporary.Write(encrypted); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write gateway data: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close gateway data file: %w", err)
	}
	if err := os.Rename(temporaryName, store.path); err != nil {
		return fmt.Errorf("replace gateway data: %w", err)
	}
	return nil
}

func (store *EncryptedStore) cipher() (cipher.AEAD, error) {
	block, err := aes.NewCipher(store.key)
	if err != nil {
		return nil, fmt.Errorf("create gateway cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gateway encryption: %w", err)
	}
	return gcm, nil
}
