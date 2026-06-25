package osscard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// sampleSearch is a trimmed GitHub /search/issues response with two merged PRs.
const sampleSearch = `{
  "total_count": 2,
  "items": [
    {
      "number": 4989,
      "title": "support -t -d -i",
      "html_url": "https://github.com/containerd/nerdctl/pull/4989",
      "closed_at": "2024-05-01T10:00:00Z",
      "repository_url": "https://api.github.com/repos/containerd/nerdctl",
      "pull_request": { "merged_at": "2024-05-02T09:00:00Z" }
    },
    {
      "number": 13539,
      "title": "max_user_namespaces & <enforcement>",
      "html_url": "https://github.com/google/gvisor/pull/13539",
      "closed_at": "2024-04-01T10:00:00Z",
      "repository_url": "https://api.github.com/repos/google/gvisor",
      "pull_request": { "merged_at": "2024-04-01T10:00:00Z" }
    }
  ]
}`

// TestParseSearch checks that repo, number, title, URL and merge time are decoded,
// and that pull_request.merged_at is preferred over closed_at for ordering.
func TestParseSearch(t *testing.T) {
	prs, err := parseSearch([]byte(sampleSearch))
	if err != nil {
		t.Fatalf("parseSearch: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("want 2 PRs, got %d", len(prs))
	}
	got := prs[0]
	if got.Repo != "containerd/nerdctl" {
		t.Errorf("repo = %q", got.Repo)
	}
	if got.Number != 4989 {
		t.Errorf("number = %d", got.Number)
	}
	if got.Title != "support -t -d -i" {
		t.Errorf("title = %q", got.Title)
	}
	if got.URL != "https://github.com/containerd/nerdctl/pull/4989" {
		t.Errorf("url = %q", got.URL)
	}
	// merged_at (May 2) must win over closed_at (May 1).
	if want := time.Date(2024, 5, 2, 9, 0, 0, 0, time.UTC); !got.Merged.Equal(want) {
		t.Errorf("merged = %v, want %v", got.Merged, want)
	}
}

// TestParseSearchInvalid checks that malformed JSON is reported as an error.
func TestParseSearchInvalid(t *testing.T) {
	if _, err := parseSearch([]byte("{not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestSelectRecent checks dedupe by URL, newest-first ordering, and the limit cap.
func TestSelectRecent(t *testing.T) {
	may := time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC)
	apr := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	jun := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	in := []PR{
		{Repo: "a/a", Number: 1, URL: "u1", Merged: may},
		{Repo: "b/b", Number: 2, URL: "u2", Merged: apr},
		{Repo: "a/a", Number: 1, URL: "u1", Merged: may}, // duplicate of u1
		{Repo: "c/c", Number: 3, URL: "u3", Merged: jun},
	}
	out := selectRecent(in, 2)
	if len(out) != 2 {
		t.Fatalf("want 2 after dedupe+limit, got %d", len(out))
	}
	// Newest first: jun (u3) then may (u1).
	if out[0].URL != "u3" || out[1].URL != "u1" {
		t.Errorf("order = %q,%q; want u3,u1", out[0].URL, out[1].URL)
	}
}

// TestSelectRecentLimitZeroKeepsAll checks that a non-positive limit returns every PR.
func TestSelectRecentLimitZeroKeepsAll(t *testing.T) {
	in := []PR{{URL: "u1"}, {URL: "u2"}}
	if got := selectRecent(in, 0); len(got) != 2 {
		t.Fatalf("limit 0 should keep all, got %d", len(got))
	}
}

// TestRenderSVG checks the card carries the title and each PR's repo, number and title.
func TestRenderSVG(t *testing.T) {
	prs := []PR{
		{Repo: "containerd/nerdctl", Number: 4989, Title: "support flags", URL: "x", Merged: time.Now()},
	}
	svg := RenderSVG(prs, "Recent OSS")
	if !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("output is not an <svg: %.20q", svg)
	}
	for _, want := range []string{"Recent OSS", "containerd/nerdctl", "#4989", "support flags", "</svg>"} {
		if !strings.Contains(svg, want) {
			t.Errorf("svg missing %q", want)
		}
	}
}

// TestRenderSVGEscapes checks that XML metacharacters in a title are escaped.
func TestRenderSVGEscapes(t *testing.T) {
	prs := []PR{{Repo: "a/b", Number: 1, Title: `a < b & "c"`, URL: "x", Merged: time.Now()}}
	svg := RenderSVG(prs, "T")
	if strings.Contains(svg, "a < b & \"c\"") {
		t.Error("raw metacharacters leaked into SVG")
	}
	if !strings.Contains(svg, "&lt;") || !strings.Contains(svg, "&amp;") {
		t.Error("expected escaped entities for < and &")
	}
}

// TestRenderSVGEmpty checks that an empty list still yields a valid card with a placeholder.
func TestRenderSVGEmpty(t *testing.T) {
	svg := RenderSVG(nil, "T")
	if !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "</svg>") {
		t.Fatal("empty render is not a valid svg")
	}
	if !strings.Contains(strings.ToLower(svg), "no ") {
		t.Error("empty render should show a placeholder line")
	}
}

// TestRenderSVGHeightGrows checks that taller cards are produced for more rows.
func TestRenderSVGHeightGrows(t *testing.T) {
	one := RenderSVG([]PR{{Repo: "a/b", Number: 1, Title: "t"}}, "T")
	three := RenderSVG([]PR{
		{Repo: "a/b", Number: 1, Title: "t"},
		{Repo: "a/b", Number: 2, Title: "t"},
		{Repo: "a/b", Number: 3, Title: "t"},
	}, "T")
	if heightOf(t, one) >= heightOf(t, three) {
		t.Error("card height should grow with row count")
	}
}

// heightOf extracts the integer value of the svg height attribute for comparison.
func heightOf(t *testing.T, svg string) int {
	t.Helper()
	const key = `height="`
	i := strings.Index(svg, key)
	if i < 0 {
		t.Fatal("no height attribute")
	}
	rest := svg[i+len(key):]
	end := strings.IndexByte(rest, '"')
	n := 0
	for _, r := range rest[:end] {
		n = n*10 + int(r-'0')
	}
	return n
}

// TestFetch checks that Fetch queries each scope and aggregates results newest-first.
func TestFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		// Each scope returns a distinct repo so we can assert aggregation.
		switch {
		case strings.Contains(q, "repo:containerd/nerdctl"):
			w.Write([]byte(`{"items":[{"number":1,"title":"a","html_url":"u1","repository_url":"https://api.github.com/repos/containerd/nerdctl","pull_request":{"merged_at":"2024-05-01T00:00:00Z"}}]}`))
		case strings.Contains(q, "org:llm-d"):
			w.Write([]byte(`{"items":[{"number":2,"title":"b","html_url":"u2","repository_url":"https://api.github.com/repos/llm-d/llm-d","pull_request":{"merged_at":"2024-06-01T00:00:00Z"}}]}`))
		default:
			w.Write([]byte(`{"items":[]}`))
		}
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
	prs, err := c.Fetch(context.Background(), Config{
		User:   "mayur-tolexo",
		Scopes: []string{"repo:containerd/nerdctl", "org:llm-d"},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(prs) != 2 {
		t.Fatalf("want 2 aggregated PRs, got %d", len(prs))
	}
	// llm-d PR merged in June must sort ahead of the nerdctl PR merged in May.
	if prs[0].Repo != "llm-d/llm-d" {
		t.Errorf("newest-first ordering broken: %q", prs[0].Repo)
	}
}

// TestFetchHTTPError checks that a non-200 status from the API is surfaced as an error.
func TestFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()

	c := &Client{HTTP: srv.Client(), BaseURL: srv.URL}
	_, err := c.Fetch(context.Background(), Config{User: "u", Scopes: []string{"repo:x/y"}, Limit: 5})
	if err == nil {
		t.Fatal("expected error on HTTP 403")
	}
}
