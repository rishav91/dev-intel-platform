// Package githubapp implements GitHub App authentication (P1.A): it mints the
// app-level JWT, exchanges it for short-lived per-installation access tokens
// (cached + refreshed), and tracks per-installation rate budget. All GitHub API
// access in later phases (enrichment P1.C, backfill P1.G) goes through here.
//
// We never write to GitHub — every requested permission is read-only (GITHUB-APP.md).
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
	"errors"
	"fmt"
	"time"
)

// AppCredentials is a GitHub App's identity: its numeric app id and RSA private
// key. The key is loaded from PEM — in production sourced from Vault (NFR-6.2);
// in dev from a file/env. Keeping the loader separate is the Vault seam.
type AppCredentials struct {
	AppID      int64
	privateKey *rsa.PrivateKey
}

// NewAppCredentials parses a PKCS#1 or PKCS#8 RSA private key in PEM form.
func NewAppCredentials(appID int64, pemBytes []byte) (*AppCredentials, error) {
	if appID == 0 {
		return nil, errors.New("githubapp: app id is zero")
	}
	key, err := parseRSAPrivateKey(pemBytes)
	if err != nil {
		return nil, err
	}
	return &AppCredentials{AppID: appID, privateKey: key}, nil
}

func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("githubapp: no PEM block in private key")
	}
	// GitHub issues PKCS#1 ("RSA PRIVATE KEY"); accept PKCS#8 too for portability.
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubapp: parse private key: %w", err)
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("githubapp: private key is %T, want RSA", keyAny)
	}
	return key, nil
}

// AppJWT mints a short-lived app JWT (RS256) per GitHub's spec: iss = app id,
// iat backdated 60s for clock skew, exp = now + ttl (GitHub caps at 10 min).
// Used as the bearer token when requesting installation access tokens.
func (c *AppCredentials) AppJWT(now time.Time, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > 10*time.Minute {
		ttl = 10 * time.Minute
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(ttl).Unix(),
		"iss": c.AppID,
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64(hb) + "." + b64(cb)

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("githubapp: sign jwt: %w", err)
	}
	return signingInput + "." + b64(sig), nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
