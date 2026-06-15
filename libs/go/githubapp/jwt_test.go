package githubapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"
)

func testKeyPEM(t *testing.T) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return key, pemBytes
}

func TestAppJWT(t *testing.T) {
	key, pemBytes := testKeyPEM(t)
	creds, err := NewAppCredentials(99, pemBytes)
	if err != nil {
		t.Fatalf("creds: %v", err)
	}

	now := time.Unix(1_700_000_000, 0)
	tok, err := creds.AppJWT(now, 10*time.Minute)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("want 3 JWT segments, got %d", len(parts))
	}

	// Signature must verify against the public key over header.payload.
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Fatalf("signature does not verify: %v", err)
	}

	// Claims: iss = app id, iat backdated 60s, exp = iat window.
	var claims struct {
		Iat int64 `json:"iat"`
		Exp int64 `json:"exp"`
		Iss int64 `json:"iss"`
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	if claims.Iss != 99 {
		t.Errorf("iss: want 99 got %d", claims.Iss)
	}
	if claims.Iat != now.Add(-60*time.Second).Unix() {
		t.Errorf("iat: want %d got %d", now.Add(-60*time.Second).Unix(), claims.Iat)
	}
	if claims.Exp != now.Add(10*time.Minute).Unix() {
		t.Errorf("exp: want %d got %d", now.Add(10*time.Minute).Unix(), claims.Exp)
	}
}

func TestAppJWTCapsTTL(t *testing.T) {
	_, pemBytes := testKeyPEM(t)
	creds, _ := NewAppCredentials(1, pemBytes)
	now := time.Unix(1_700_000_000, 0)
	// GitHub caps app JWT lifetime at 10 min; an over-long ttl is clamped.
	tok, err := creds.AppJWT(now, time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	parts := strings.Split(tok, ".")
	var claims struct {
		Exp int64 `json:"exp"`
	}
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	_ = json.Unmarshal(cb, &claims)
	if claims.Exp != now.Add(10*time.Minute).Unix() {
		t.Errorf("ttl not clamped to 10m: exp=%d", claims.Exp)
	}
}

func TestNewAppCredentialsErrors(t *testing.T) {
	_, pemBytes := testKeyPEM(t)
	if _, err := NewAppCredentials(0, pemBytes); err == nil {
		t.Error("expected error for zero app id")
	}
	if _, err := NewAppCredentials(1, []byte("not a pem")); err == nil {
		t.Error("expected error for invalid PEM")
	}
}
