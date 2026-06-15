// ghcheck is a dev smoke tool that makes a REAL GitHub call through the P1.A
// auth + budget stack: it mints an installation token and detects a repo's
// capabilities, printing the token metadata and the remaining rate budget. It's
// the manual counterpart to the env-gated integration test — handy for confirming
// a freshly registered App + installation actually works.
//
//	export GITHUB_APP_ID=123456
//	export GITHUB_APP_PRIVATE_KEY_PATH=./secrets/github-app.pem
//	go run ./cmd/ghcheck -installation 4242 -repo owner/name
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/dev-intel/platform/libs/go/githubapp"
)

func main() {
	list := flag.Bool("list", false, "list this App's installations (id + account) and exit")
	installation := flag.Int64("installation", 0, "GitHub App installation id")
	repo := flag.String("repo", "", "owner/name to probe for capabilities")
	flag.Parse()

	tokens, client, err := githubapp.LoadFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if *list {
		installs, err := tokens.ListInstallations(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "list installations:", err)
			os.Exit(1)
		}
		if len(installs) == 0 {
			fmt.Println("no installations yet — install the App on an account/repo first")
			return
		}
		fmt.Println("installations:")
		for _, in := range installs {
			fmt.Printf("  id=%-12d account=%s\n", in.ID, in.Account)
		}
		return
	}

	if *installation == 0 || *repo == "" {
		fmt.Fprintln(os.Stderr, "usage: ghcheck -list | ghcheck -installation <id> -repo <owner/name>")
		os.Exit(2)
	}

	tok, err := tokens.Token(ctx, *installation)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mint token:", err)
		os.Exit(1)
	}
	fmt.Printf("✓ installation token minted (expires %s)\n", tok.ExpiresAt.Format(time.RFC3339))
	fmt.Printf("  granted permissions: %v\n", tok.Permissions)

	caps, err := client.DetectRepoCapabilities(ctx, *installation, *repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "detect capabilities:", err)
		os.Exit(1)
	}
	fmt.Printf("✓ capabilities for %s: deployments=%v releases=%v\n", *repo, caps.Deployments, caps.Releases)

	if rem, known := client.Budget(*installation).Remaining(githubapp.REST); known {
		fmt.Printf("  REST budget remaining: %d\n", rem)
	}
}
