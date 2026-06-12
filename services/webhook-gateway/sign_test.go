package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidSignature(t *testing.T) {
	secret := []byte("dev-secret")
	body := []byte(`{"action":"opened"}`)
	good := sign(secret, body)

	cases := []struct {
		name   string
		secret []byte
		header string
		body   []byte
		want   bool
	}{
		{"valid", secret, good, body, true},
		{"wrong secret", []byte("other"), good, body, false},
		{"tampered body", secret, good, []byte(`{"action":"closed"}`), false},
		{"missing prefix", secret, hex.EncodeToString([]byte("x")), body, false},
		{"bad hex", secret, "sha256=zzzz", body, false},
		{"empty header", secret, "", body, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := validSignature(tc.secret, tc.header, tc.body); got != tc.want {
				t.Fatalf("validSignature = %v, want %v", got, tc.want)
			}
		})
	}
}
