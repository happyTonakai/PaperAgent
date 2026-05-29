package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const encPrefix = "!!aes:"

// keyPath returns path to the AES key file.
func keyPath() string {
	return filepath.Join(ConfigDir(), ".key")
}

// loadOrCreateKey reads the AES key from disk, or generates a new one.
func loadOrCreateKey() ([]byte, error) {
	path := keyPath()
	data, err := os.ReadFile(path)
	if err == nil && len(data) >= 32 {
		return data[:32], nil
	}
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	// Generate new 256-bit key
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}

	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}

	if err := os.WriteFile(path, []byte(hex.EncodeToString(key)), 0600); err != nil {
		return nil, fmt.Errorf("write key file: %w", err)
	}

	return key, nil
}

// encryptKey encrypts plaintext with AES-256-GCM using a random IV.
// Returns the encPrefix + base64(iv || ciphertext) string.
func encryptKey(plaintext string) (string, error) {
	key, err := loadOrCreateKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("generate iv: %w", err)
	}

	out := gcm.Seal(iv, iv, []byte(plaintext), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// decryptKey decrypts a string produced by encryptKey.
// If the value doesn't start with encPrefix, it's returned as-is (plaintext).
func decryptKey(stored string) (string, error) {
	if !hasEncPrefix(stored) {
		return stored, nil
	}

	encoded := stored[len(encPrefix):]
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("decode encrypted key: %w", err)
	}

	key, err := loadOrCreateKey()
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}

	iv := data[:nonceSize]
	ciphertext := data[nonceSize:]

	plain, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt api key: %w", err)
	}

	return string(plain), nil
}

// hasEncPrefix reports whether s was encrypted by encryptKey.
func hasEncPrefix(s string) bool {
	return len(s) >= len(encPrefix) && s[:len(encPrefix)] == encPrefix
}
