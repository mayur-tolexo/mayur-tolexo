// Package osscard renders a self-contained SVG card listing a developer's most
// recent merged pull requests in upstream open-source repositories, sourced live
// from the GitHub search API. It exists so the profile shows real contributions
// without leaking private or self-owned work: callers pass an explicit allow-list
// of public scopes (repos and orgs) to search.
package osscard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// PR is a single merged pull request authored in an upstream repository.
type PR struct {
	Repo   string    // "owner/name", e.g. "containerd/nerdctl"
	Number int       // pull request number
	Title  string    // pull request title, rendered escaped
	URL    string    // html_url, also the dedupe key
	Merged time.Time // merge time, used to order contributions newest-first
}

// searchResponse mirrors the subset of GitHub's /search/issues payload we read.
type searchResponse struct {
	Items []struct {
		Number        int    `json:"number"`
		Title         string `json:"title"`
		HTMLURL       string `json:"html_url"`
		ClosedAt      string `json:"closed_at"`
		RepositoryURL string `json:"repository_url"`
		PullRequest   *struct {
			MergedAt string `json:"merged_at"`
		} `json:"pull_request"`
	} `json:"items"`
}

// parseSearch decodes a /search/issues response body into PRs. It derives the
// "owner/name" repo from repository_url and prefers pull_request.merged_at over
// closed_at as the merge time, since the former is the precise merge instant.
func parseSearch(body []byte) ([]PR, error) {
	var resp searchResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}
	prs := make([]PR, 0, len(resp.Items))
	for _, it := range resp.Items {
		// Prefer the explicit merge timestamp; fall back to closed_at.
		ts := it.ClosedAt
		if it.PullRequest != nil && it.PullRequest.MergedAt != "" {
			ts = it.PullRequest.MergedAt
		}
		merged, _ := time.Parse(time.RFC3339, ts) // zero time if absent/unparseable
		prs = append(prs, PR{
			Repo:   repoFromURL(it.RepositoryURL),
			Number: it.Number,
			Title:  it.Title,
			URL:    it.HTMLURL,
			Merged: merged,
		})
	}
	return prs, nil
}

// repoFromURL turns an api repository_url into "owner/name" by taking everything
// after the "/repos/" segment, independent of the API host.
func repoFromURL(apiURL string) string {
	const marker = "/repos/"
	if i := strings.Index(apiURL, marker); i >= 0 {
		return apiURL[i+len(marker):]
	}
	return apiURL
}

// selectRecent dedupes PRs by URL, orders them newest-merged first, and caps the
// result to limit. A non-positive limit keeps every (deduped) PR.
func selectRecent(prs []PR, limit int) []PR {
	seen := make(map[string]bool, len(prs))
	out := make([]PR, 0, len(prs))
	for _, pr := range prs {
		if seen[pr.URL] {
			continue
		}
		seen[pr.URL] = true
		out = append(out, pr)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Merged.After(out[j].Merged)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Config controls a single Fetch run.
type Config struct {
	User   string   // GitHub login whose authored PRs are searched
	Scopes []string // search qualifiers, e.g. "repo:containerd/nerdctl" or "org:llm-d"
	Limit  int      // maximum PRs to keep across all scopes
}

// Client queries the GitHub search API for merged pull requests.
type Client struct {
	HTTP    *http.Client // defaults to http.DefaultClient when nil
	BaseURL string       // defaults to https://api.github.com
	Token   string       // optional bearer token to raise rate limits
}

// Fetch returns the most recent merged PRs cfg.User authored across cfg.Scopes,
// querying each scope separately and merging the results. Restricting to caller-
// supplied public scopes is what keeps private and self-owned repos off the card.
func (c *Client) Fetch(ctx context.Context, cfg Config) ([]PR, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://api.github.com"
	}
	var all []PR
	for _, scope := range cfg.Scopes {
		q := fmt.Sprintf("author:%s type:pr is:merged %s", cfg.User, scope)
		prs, err := c.search(ctx, base, q)
		if err != nil {
			return nil, err
		}
		all = append(all, prs...)
	}
	return selectRecent(all, cfg.Limit), nil
}

// search performs one /search/issues request for query q and parses the result.
func (c *Client) search(ctx context.Context, base, q string) ([]PR, error) {
	u := base + "/search/issues?" + url.Values{
		"q":        {q},
		"sort":     {"updated"},
		"order":    {"desc"},
		"per_page": {"20"},
	}.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// A non-200 means a bad query, auth, or rate limit; surface it rather than
	// silently rendering an empty card.
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github search %q: status %d: %s", q, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseSearch(body)
}

// tokyonight palette, matched to the rest of the profile's cards.
const (
	colBG     = "#1a1b27"
	colBorder = "#29304a"
	colTitle  = "#7aa2f7"
	colRepo   = "#9ece6a"
	colText   = "#c0caf5"
	colMuted  = "#565f89"
)

// SVG layout constants in user-space pixels.
const (
	cardWidth = 480
	padX      = 20
	titleY    = 34
	firstRowY = 66
	rowHeight = 28
	bottomPad = 16
)

// RenderSVG draws a tokyonight-themed card titled title that lists prs, one per
// row as "owner/name #number  title". An empty list renders a placeholder line so
// the card never appears broken. All text is XML-escaped.
func RenderSVG(prs []PR, title string) string {
	rows := len(prs)
	if rows == 0 {
		rows = 1 // reserve a line for the placeholder
	}
	height := firstRowY + rows*rowHeight + bottomPad

	var b strings.Builder
	fmt.Fprintf(&b, `<svg width="%d" height="%d" viewBox="0 0 %d %d" xmlns="http://www.w3.org/2000/svg">`,
		cardWidth, height, cardWidth, height)
	// Card background and border.
	fmt.Fprintf(&b, `<rect x="0.5" y="0.5" width="%d" height="%d" rx="10" fill="%s" stroke="%s"/>`,
		cardWidth-1, height-1, colBG, colBorder)
	// Title.
	fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="Segoe UI,Helvetica,Arial,sans-serif" font-size="16" font-weight="600" fill="%s">%s</text>`,
		padX, titleY, colTitle, escape(title))

	if len(prs) == 0 {
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="monospace" font-size="13" fill="%s">no merged contributions yet</text>`,
			padX, firstRowY, colMuted)
	}
	for i, pr := range prs {
		y := firstRowY + i*rowHeight
		// "owner/name #num" prefix in the repo color, then the title in body text.
		prefix := fmt.Sprintf("%s #%d", pr.Repo, pr.Number)
		fmt.Fprintf(&b, `<text x="%d" y="%d" font-family="monospace" font-size="13">`, padX, y)
		fmt.Fprintf(&b, `<tspan fill="%s">%s</tspan>`, colRepo, escape(prefix))
		fmt.Fprintf(&b, `<tspan fill="%s">  %s</tspan>`, colText, escape(truncate(pr.Title, 52)))
		b.WriteString(`</text>`)
	}
	b.WriteString(`</svg>`)
	return b.String()
}

// escape renders s safe for XML text/attribute content.
func escape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return r.Replace(s)
}

// truncate shortens s to at most max runes, appending an ellipsis when cut, so
// long PR titles don't overflow the card width.
func truncate(s string, max int) string {
	rs := []rune(s)
	if len(rs) <= max {
		return s
	}
	return string(rs[:max-1]) + "…"
}
