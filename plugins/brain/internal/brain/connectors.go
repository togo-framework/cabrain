package brain

// Built-in connectors. Each registers a kind into the connector registry. They use
// only the stdlib (+ the pgx SQL driver) so no new heavy deps. pdf/image/mcp are
// follow-ups (media extraction + external MCP calls).

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // "pgx" driver for the sql connector
)

func init() {
	RegisterConnector("text", ConnectorFunc(textConnector))
	RegisterConnector("markdown", ConnectorFunc(textConnector)) // same path; markdown is just text
	RegisterConnector("crawler", ConnectorFunc(crawlerConnector))
	RegisterConnector("github", ConnectorFunc(githubConnector))
	RegisterConnector("sql", ConnectorFunc(sqlConnector))
}

func cfgStr(cfg map[string]any, k string) string {
	if v, ok := cfg[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

// --- text / markdown: config { content, title? } -----------------------------

func textConnector(_ context.Context, cfg map[string]any, _ string) ([]Document, string, error) {
	content := cfgStr(cfg, "content")
	if content == "" {
		return nil, "", errors.New("text source: config.content is empty")
	}
	meta := map[string]any{"type": "doc"}
	if t := cfgStr(cfg, "title"); t != "" {
		meta["title"] = t
	}
	return []Document{{Content: content, SourceRef: cfgStr(cfg, "title"), Metadata: meta}}, "", nil
}

// --- crawler: config { url } — fetch a page, strip to text --------------------

var (
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style|noscript)[^>]*>.*?</\s*(script|style|noscript)\s*>`)
	reTag         = regexp.MustCompile(`(?s)<[^>]+>`)
	reWs          = regexp.MustCompile(`[ \t]+`)
	reBlankLines  = regexp.MustCompile(`\n{3,}`)
	reTitle       = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

func htmlToText(html string) string {
	html = reScriptStyle.ReplaceAllString(html, " ")
	html = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "<br />", "\n",
		"</p>", "\n\n", "</div>", "\n", "</li>", "\n", "</h1>", "\n\n",
		"</h2>", "\n\n", "</h3>", "\n\n").Replace(html)
	html = reTag.ReplaceAllString(html, "")
	html = htmlUnescape(html)
	html = reWs.ReplaceAllString(html, " ")
	html = reBlankLines.ReplaceAllString(html, "\n\n")
	return strings.TrimSpace(html)
}

func htmlUnescape(s string) string {
	return strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`,
		"&#39;", "'", "&nbsp;", " ", "&mdash;", "—", "&rsquo;", "'").Replace(s)
}

func crawlerConnector(ctx context.Context, cfg map[string]any, _ string) ([]Document, string, error) {
	url := cfgStr(cfg, "url")
	if url == "" {
		return nil, "", errors.New("crawler source: config.url is empty")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "CaBrain-Crawler/1.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("crawler: %s -> http %d", url, resp.StatusCode)
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	title := url
	if m := reTitle.FindStringSubmatch(string(raw)); len(m) == 2 {
		title = strings.TrimSpace(htmlUnescape(m[1]))
	}
	text := htmlToText(string(raw))
	if text == "" {
		return nil, "", errors.New("crawler: no text extracted")
	}
	return []Document{{ExternalID: url, Content: text, SourceRef: url,
		Metadata: map[string]any{"type": "doc", "title": title, "url": url}}}, "", nil
}

// --- github: config { repo, branch?, path?, ext?, exclude?, maxBytes?, maxDocs?,
//                      token? } ------------------------------------------------
// Ingests text files from a repo tree (how AVO was loaded). `ext` accepts EITHER a
// single suffix (".md", the default — docs-only, the original behaviour) OR a list
// / comma-separated string of suffixes (".go,.ts,.sql") so a repo's SOURCE CODE can
// be indexed without creating one datasource per extension.
//
// Indexing code needs three guards that docs never did, or a sync drowns the brain
// in vendored/minified/generated text:
//   - `exclude`: extra path substrings to drop, ON TOP of ghSkipDirs (node_modules,
//     vendor, dist, .next, testdata, …) and lock/minified/generated file names.
//   - `maxBytes`: per-file size ceiling from the tree listing (default 60_000) — a
//     500KB bundle is noise, and embedding it costs the same as 30 real files.
//   - `maxDocs`: per-sync file ceiling (default 500).
//
// Event time: every document is stamped with the repo's pushed_at instead of now(),
// so an import doesn't collapse the whole history onto the ingest date. With
// `fileDates: true` each file gets its own last-commit date (one extra API call per
// file — accurate, but only worth it under a small maxDocs).

// ghSkipDirs are path segments that never carry hand-written knowledge.
var ghSkipDirs = []string{
	"node_modules/", "vendor/", "/dist/", "dist/", "build/", ".next/", "out/",
	"testdata/", "__pycache__/", ".venv/", "coverage/", "third_party/",
	".git/", "public/build/", "storage/framework/", "__snapshots__/",
}

// ghSkipFile matches generated / lock / minified files by name.
func ghSkipFile(p string) bool {
	lp := strings.ToLower(p)
	base := lp
	if i := strings.LastIndex(lp, "/"); i >= 0 {
		base = lp[i+1:]
	}
	switch {
	case strings.HasSuffix(base, ".lock"), strings.HasSuffix(base, "-lock.json"),
		base == "package-lock.json", base == "yarn.lock", base == "composer.lock",
		base == "pnpm-lock.yaml", base == "go.sum":
		return true
	case strings.Contains(base, ".min."), strings.Contains(base, ".gen."),
		strings.HasSuffix(base, ".generated.ts"), strings.HasSuffix(base, "_test.go"),
		strings.HasSuffix(base, ".d.ts"):
		return true
	}
	for _, d := range ghSkipDirs {
		if strings.Contains(lp, d) {
			return true
		}
	}
	return false
}

// cfgList reads a config value that may be a JSON array, a comma-separated string,
// or a single string, and returns it as a trimmed, lower-cased slice.
func cfgList(cfg map[string]any, k string) []string {
	var out []string
	add := func(s string) {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	switch v := cfg[k].(type) {
	case string:
		for _, p := range strings.Split(v, ",") {
			add(p)
		}
	case []string:
		for _, p := range v {
			add(p)
		}
	case []any:
		for _, p := range v {
			if s, ok := p.(string); ok {
				add(s)
			}
		}
	}
	return out
}

func cfgInt(cfg map[string]any, k string, def int) int {
	switch v := cfg[k].(type) {
	case float64:
		if int(v) > 0 {
			return int(v)
		}
	case int:
		if v > 0 {
			return v
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func githubConnector(ctx context.Context, cfg map[string]any, _ string) ([]Document, string, error) {
	repo := cfgStr(cfg, "repo") // owner/name
	if repo == "" {
		return nil, "", errors.New("github source: config.repo (owner/name) is empty")
	}
	token := cfgStr(cfg, "token")
	branch := cfgStr(cfg, "branch")
	pushedAt, defBranch := ghRepoMeta(ctx, repo, token)
	if branch == "" {
		branch = defBranch
	}
	if branch == "" {
		branch = "main"
	}
	pathPrefix := cfgStr(cfg, "path")
	exts := cfgList(cfg, "ext")
	if len(exts) == 0 {
		exts = []string{".md"}
	}
	exclude := cfgList(cfg, "exclude")
	maxBytes := cfgInt(cfg, "maxBytes", 60000)
	maxDocs := cfgInt(cfg, "maxDocs", 500)
	fileDates, _ := cfg["fileDates"].(bool)

	treeURL := fmt.Sprintf("https://api.github.com/repos/%s/git/trees/%s?recursive=1", repo, branch)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, treeURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, "", fmt.Errorf("github tree %s@%s -> http %d: %s", repo, branch, resp.StatusCode, string(b))
	}
	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			Size int    `json:"size"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, "", err
	}
	var docs []Document
	for _, e := range tree.Tree {
		if e.Type != "blob" {
			continue
		}
		lp := strings.ToLower(e.Path)
		matched := false
		for _, x := range exts {
			if strings.HasSuffix(lp, x) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		if pathPrefix != "" && !strings.HasPrefix(e.Path, pathPrefix) {
			continue
		}
		if ghSkipFile(e.Path) {
			continue
		}
		skip := false
		for _, x := range exclude {
			if strings.Contains(lp, x) {
				skip = true
				break
			}
		}
		if skip || (e.Size > 0 && e.Size > maxBytes) {
			continue
		}
		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repo, branch, e.Path)
		content, err := fetchText(ctx, rawURL, token)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		validAt := pushedAt
		if fileDates {
			if t := ghFileDate(ctx, repo, e.Path, token); t != nil {
				validAt = t
			}
		}
		docs = append(docs, Document{
			ExternalID: rawURL, Content: content, SourceRef: repo + "/" + e.Path,
			ValidAt:  validAt,
			Metadata: map[string]any{"type": "doc", "title": e.Path, "repo": repo, "path": e.Path},
		})
		if len(docs) >= maxDocs { // safety cap per sync
			break
		}
	}
	if len(docs) == 0 {
		return nil, "", fmt.Errorf("github: no %v files under %q in %s@%s", exts, pathPrefix, repo, branch)
	}
	return docs, "", nil
}

// ghRepoMeta returns the repo's pushed_at (event time for its files) and default
// branch. Best-effort: on any error the caller falls back to now()/"main".
func ghRepoMeta(ctx context.Context, repo, token string) (*time.Time, string) {
	var out struct {
		PushedAt      time.Time `json:"pushed_at"`
		DefaultBranch string    `json:"default_branch"`
	}
	if err := ghJSON(ctx, "https://api.github.com/repos/"+repo, token, &out); err != nil {
		return nil, ""
	}
	if out.PushedAt.IsZero() {
		return nil, out.DefaultBranch
	}
	return &out.PushedAt, out.DefaultBranch
}

// ghFileDate returns a file's last-commit date (one API call).
func ghFileDate(ctx context.Context, repo, path, token string) *time.Time {
	var out []struct {
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	u := fmt.Sprintf("https://api.github.com/repos/%s/commits?per_page=1&path=%s", repo, url.QueryEscape(path))
	if err := ghJSON(ctx, u, token, &out); err != nil || len(out) == 0 {
		return nil
	}
	d := out[0].Commit.Committer.Date
	if d.IsZero() {
		return nil
	}
	return &d
}

func ghJSON(ctx context.Context, u, token string, dst any) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("github: %s -> http %d", u, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

func fetchText(ctx context.Context, url, token string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return string(b), nil
}

// --- sql: config { driver?, dsn, query, refColumn?, titleColumn? } ------------
// Runs a query against an external DB and turns each row into a document. This is
// how the FlowOS hub's github-activity / claude-activity tables get pulled into a
// brain — e.g. query "SELECT id, repo, actor, action, created_at FROM github_activity".

func sqlConnector(ctx context.Context, cfg map[string]any, cursor string) ([]Document, string, error) {
	driver := cfgStr(cfg, "driver")
	if driver == "" {
		driver = "pgx"
	}
	dsn := cfgStr(cfg, "dsn")
	query := cfgStr(cfg, "query")
	if dsn == "" || query == "" {
		return nil, "", errors.New("sql source: config.dsn and config.query required")
	}
	refCol := cfgStr(cfg, "refColumn")
	titleCol := cfgStr(cfg, "titleColumn")

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, "", fmt.Errorf("sql open: %w", err)
	}
	defer db.Close()
	db.SetConnMaxLifetime(20 * time.Second)
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	rows, err := db.QueryContext(cctx, query)
	if err != nil {
		return nil, "", fmt.Errorf("sql query: %w", err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, "", err
	}
	var docs []Document
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, "", err
		}
		var b strings.Builder
		var ref, title string
		for i, c := range cols {
			v := stringifySQL(vals[i])
			if c == refCol {
				ref = v
			}
			if c == titleCol {
				title = v
			}
			if v == "" {
				continue
			}
			fmt.Fprintf(&b, "%s: %s\n", c, v)
		}
		content := strings.TrimSpace(b.String())
		if content == "" {
			continue
		}
		meta := map[string]any{"type": "row"}
		if title != "" {
			meta["title"] = title
		}
		docs = append(docs, Document{ExternalID: ref, Content: content, SourceRef: ref, Metadata: meta})
		if len(docs) >= 5000 { // safety cap per sync
			break
		}
	}
	return docs, cursor, rows.Err()
}

func stringifySQL(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(x)
	}
}

// base64 import kept for future media connectors (pdf/image); referenced here so
// the import doesn't break builds when those land.
var _ = base64.StdEncoding
