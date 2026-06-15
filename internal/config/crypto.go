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
	"strings"
)

const encPrefix = "!!aes:"

// keyPath returns path to the AES key file.
func keyPath() string {
	return filepath.Join(ConfigDir(), ".key")
}

// loadOrCreateKey reads the AES key from disk (hex-decoding it), or generates a new one.
func loadOrCreateKey() ([]byte, error) {
	path := keyPath()
	data, err := os.ReadFile(path)
	if err == nil && len(data) >= 64 {
		// Key file stores hex-encoded 32 random bytes (64 hex chars).
		// Decode the hex string to recover the original 32-byte key.
		hexStr := string(data)
		// Strip any trailing whitespace/newlines
		hexStr = strings.TrimSpace(hexStr)
		if decoded, hexErr := hex.DecodeString(hexStr); hexErr == nil && len(decoded) == 32 {
			return decoded, nil
		}
		// Legacy fallback: old code incorrectly used raw hex-ASCII bytes.
		// Try that as a backup for backward compatibility with keys encrypted
		// by the buggy version. We still prefer the decoded key above.
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

// loadLegacyKey returns the old (buggy) key for backward compatibility.
// Before the hex-decode fix, the code used data[:32] on the 64-byte hex file,
// which gave the ASCII bytes of the first 32 hex characters as the key.
func loadLegacyKey() ([]byte, error) {
	path := keyPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 32 {
		return nil, fmt.Errorf("key file too short: %d bytes", len(data))
	}
	return data[:32], nil
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
// Tries the correct (hex-decoded) key first; falls back to the legacy key
// for backward compatibility with keys encrypted by the buggy version.
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

	// Try the correct (hex-decoded) key first
	plain, err := aesGCMDecrypt(key, data)
	if err == nil {
		return string(plain), nil
	}

	// Fallback: try the legacy key (raw hex-ASCII bytes from the buggy version)
	legacyKey, legacyErr := loadLegacyKey()
	if legacyErr == nil {
		if plain, err2 := aesGCMDecrypt(legacyKey, data); err2 == nil {
			// Successfully decrypted with legacy key.
			// The next Save() will re-encrypt with the correct key.
			return string(plain), nil
		}
	}

	return "", fmt.Errorf("decrypt api key: %w", err)
}

// aesGCMDecrypt performs AES-GCM decryption with the given key.
func aesGCMDecrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}

	iv := data[:nonceSize]
	ciphertext := data[nonceSize:]

	return gcm.Open(nil, iv, ciphertext, nil)
}

// hasEncPrefix reports whether s was encrypted by encryptKey.
func hasEncPrefix(s string) bool {
	return len(s) >= len(encPrefix) && s[:len(encPrefix)] == encPrefix
}
