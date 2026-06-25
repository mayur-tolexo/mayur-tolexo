// Command osscard generates the "Recent OSS contributions" SVG for the profile
// README. It searches a fixed allow-list of upstream public scopes for the user's
// merged pull requests and writes a tokyonight-themed card to the output path.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"
	"time"

	"osscard"
)

// main parses flags, fetches the recent merged PRs, renders the card, and writes it.
func main() {
	user := flag.String("user", "mayur-tolexo", "GitHub login to search authored PRs for")
	out := flag.String("out", "oss-prs.svg", "output SVG path")
	limit := flag.Int("limit", 6, "maximum PRs to show")
	title := flag.String("title", "Recent OSS contributions", "card title")
	// Public upstream scopes only; this list is what keeps private/self-owned repos off the card.
	scopes := flag.String("scopes",
		"repo:containerd/nerdctl,repo:google/gvisor,repo:containerd/containerd,org:llm-d",
		"comma-separated GitHub search scopes")
	flag.Parse()

	client := &osscard.Client{Token: os.Getenv("GITHUB_TOKEN")}
	cfg := osscard.Config{User: *user, Scopes: splitScopes(*scopes), Limit: *limit}

	// Bound the whole fetch so a hung API call can't stall the CI job indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	prs, err := client.Fetch(ctx, cfg)
	if err != nil {
		log.Fatalf("fetch contributions: %v", err)
	}
	if err := os.WriteFile(*out, []byte(osscard.RenderSVG(prs, *title)), 0o644); err != nil {
		log.Fatalf("write %s: %v", *out, err)
	}
	log.Printf("wrote %s with %d contributions", *out, len(prs))
}

// splitScopes turns a comma-separated scope flag into a trimmed, non-empty slice.
func splitScopes(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
