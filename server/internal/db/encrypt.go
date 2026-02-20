package db

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// encryptionKey is the package-level AES-256 key used by EncryptedString.
// It must be initialized once at startup via InitEncryption before any
// database operation involving encrypted fields.
var encryptionKey []byte

// InitEncryption sets the AES-256 key used to encrypt and decrypt sensitive
// fields at rest. key must be exactly 32 bytes (AES-256).
//
// Call this once during application startup, before calling db.New:
//
//	if err := db.InitEncryption([]byte(os.Getenv("ARKEEP_SECRET_KEY"))); err != nil {
//	    log.Fatal(err)
//	}
func InitEncryption(key []byte) error {
	if len(key) != 32 {
		return fmt.Errorf("db: encryption key must be exactly 32 bytes, got %d", len(key))
	}
	encryptionKey = make([]byte, 32)
	copy(encryptionKey, key)
	return nil
}

// EncryptedString is a string type that is transparently encrypted with
// AES-256-GCM before being written to the database, and decrypted after
// being read. Use it for any sensitive field (credentials, passwords, tokens).
//
// The value stored in the database is a base64-encoded string in the format:
//
//	base64(nonce + ciphertext)
//
// An empty EncryptedString is stored as an empty string without encryption.
type EncryptedString string

// Value implements driver.Valuer. Called by GORM before writing to the database.
// Encrypts the string value with AES-256-GCM and encodes it as base64.
func (e EncryptedString) Value() (driver.Value, error) {
	if e == "" {
		return "", nil
	}
	if encryptionKey == nil {
		return nil, errors.New("db: encryption key not initialized, call db.InitEncryption first")
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, fmt.Errorf("db: failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("db: failed to create GCM: %w", err)
	}

	// Generate a random nonce. A unique nonce per encryption is critical for
	// GCM security — never reuse a nonce with the same key.
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("db: failed to generate nonce: %w", err)
	}

	// Seal appends the ciphertext and authentication tag to the nonce.
	ciphertext := gcm.Seal(nonce, nonce, []byte(e), nil)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Scan implements sql.Scanner. Called by GORM after reading from the database.
// Decodes the base64 string and decrypts it with AES-256-GCM.
func (e *EncryptedString) Scan(value interface{}) error {
	if value == nil {
		*e = ""
		return nil
	}

	str, ok := value.(string)
	if !ok {
		return fmt.Errorf("db: EncryptedString.Scan: expected string, got %T", value)
	}
	if str == "" {
		*e = ""
		return nil
	}
	if encryptionKey == nil {
		return errors.New("db: encryption key not initialized, call db.InitEncryption first")
	}

	data, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return fmt.Errorf("db: failed to decode base64: %w", err)
	}

	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return fmt.Errorf("db: failed to create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("db: failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return errors.New("db: encrypted data too short to contain nonce")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("db: failed to decrypt value: %w", err)
	}

	*e = EncryptedString(plaintext)
	return nil
}