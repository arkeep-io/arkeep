package destutil

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// cryptKey is rclone's well-known static key used to "obscure" passwords in its
// configuration. rclone refuses plaintext passwords for the sftp backend and
// expects this obscured form. The key is a fixed public value copied verbatim
// from rclone's source (fs/config/obscure/obscure.go) — it provides obfuscation,
// not security, so the value never changes across rclone releases.
var cryptKey = []byte{
	0x9c, 0x93, 0x5b, 0x48, 0x73, 0x0a, 0x55, 0x4d,
	0x6b, 0xfd, 0x7c, 0x63, 0xc8, 0x86, 0xa9, 0x2b,
	0xd3, 0x90, 0x19, 0x8e, 0xb8, 0x12, 0x8a, 0xfb,
	0xf4, 0xde, 0x16, 0x2b, 0x8b, 0x95, 0xf6, 0x38,
}

// obscure encodes a plaintext password into rclone's obscured form: a random
// 16-byte IV prepended to the AES-CTR ciphertext, base64-url encoded without
// padding. The result is accepted by rclone via RCLONE_CONFIG_*_PASS and can be
// reversed with `rclone reveal`.
func obscure(plaintext string) (string, error) {
	block, err := aes.NewCipher(cryptKey)
	if err != nil {
		return "", fmt.Errorf("destutil: failed to create cipher: %w", err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("destutil: failed to read random iv: %w", err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCTR(block, iv).XORKeyStream(ciphertext, []byte(plaintext))
	return base64.RawURLEncoding.EncodeToString(append(iv, ciphertext...)), nil
}
