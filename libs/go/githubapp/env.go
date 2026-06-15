package githubapp

import (
	"fmt"
	"os"
	"strconv"

	"github.com/dev-intel/platform/libs/go/config"
)

// LoadFromEnv builds a TokenSource + Client from environment config:
//
//	GITHUB_APP_ID                 numeric GitHub App id
//	GITHUB_APP_PRIVATE_KEY_PATH   path to the App private key (PEM)
//	GITHUB_API_BASE               optional REST root (GHES); defaults to api.github.com
//
// In production the key comes from Vault (NFR-6.2); reading a PEM file is the dev
// seam. This is the single place real credentials enter the system, reused by the
// ghcheck smoke tool and the live integration test.
func LoadFromEnv() (*TokenSource, *Client, error) {
	appIDStr := config.String("GITHUB_APP_ID", "")
	keyPath := config.String("GITHUB_APP_PRIVATE_KEY_PATH", "")
	if appIDStr == "" || keyPath == "" {
		return nil, nil, fmt.Errorf("githubapp: set GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY_PATH")
	}
	appID, err := strconv.ParseInt(appIDStr, 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("githubapp: GITHUB_APP_ID %q is not numeric: %w", appIDStr, err)
	}
	pemBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("githubapp: read private key %q: %w", keyPath, err)
	}
	creds, err := NewAppCredentials(appID, pemBytes)
	if err != nil {
		return nil, nil, err
	}

	var opts []Option
	if base := config.String("GITHUB_API_BASE", ""); base != "" {
		opts = append(opts, WithAPIBase(base))
	}
	tokens := NewTokenSource(creds, opts...)
	return tokens, NewClient(tokens, NewRegistry()), nil
}
