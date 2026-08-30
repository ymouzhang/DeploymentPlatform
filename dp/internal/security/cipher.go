package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	cipherPrefix = "enc:v1:"
	aad          = "DP:ssh-password:v1"
)

type PasswordCipher struct {
	aead cipher.AEAD
}

func NewPasswordCipher(key []byte) (*PasswordCipher, error) {
	if len(key) != 32 {
		return nil, errors.New("master key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return &PasswordCipher{aead: aead}, nil
}

func (c *PasswordCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), []byte(aad))
	payload := append(nonce, sealed...)
	return cipherPrefix + base64.StdEncoding.EncodeToString(payload), nil
}

func (c *PasswordCipher) Decrypt(encoded string) ([]byte, error) {
	if !strings.HasPrefix(encoded, cipherPrefix) {
		return nil, errors.New("unsupported password ciphertext version")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, cipherPrefix))
	if err != nil || len(payload) < c.aead.NonceSize()+c.aead.Overhead() {
		return nil, errors.New("invalid password ciphertext")
	}
	nonce := payload[:c.aead.NonceSize()]
	ciphertext := payload[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertext, []byte(aad))
	if err != nil {
		return nil, errors.New("password ciphertext cannot be decrypted with this master key")
	}
	return plaintext, nil
}
