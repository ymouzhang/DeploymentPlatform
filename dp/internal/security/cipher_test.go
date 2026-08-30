package security

import (
	"bytes"
	"testing"
)

func TestPasswordCipherRoundTrip(t *testing.T) {
	cipher, err := NewPasswordCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := cipher.Encrypt("secret")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "secret" {
		t.Fatalf("got %q", plain)
	}
}

func TestPasswordCipherRejectsWrongKey(t *testing.T) {
	first, _ := NewPasswordCipher(bytes.Repeat([]byte{1}, 32))
	second, _ := NewPasswordCipher(bytes.Repeat([]byte{2}, 32))
	encrypted, _ := first.Encrypt("secret")
	if _, err := second.Decrypt(encrypted); err == nil {
		t.Fatal("expected decryption failure")
	}
}
