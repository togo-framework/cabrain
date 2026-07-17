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
	"regexp"
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

// --- github: config { repo, branch?, path?, ext?, token? } --------------------
// Ingests text/markdown files from a repo tree (how AVO was loaded).

func githubConnector(ctx context.Context, cfg map[string]any, _ string) ([]Document, string, error) {
	repo := cfgStr(cfg, "repo") // owner/name
	if repo == "" {
		return nil, "", errors.New("github source: config.repo (owner/name) is empty")
	}
	branch := cfgStr(cfg, "branch")
	if branch == "" {
		branch = "main"
	}
	pathPrefix := cfgStr(cfg, "path")
	ext := cfgStr(cfg, "ext")
	if ext == "" {
		ext = ".md"
	}
	token := cfgStr(cfg, "token")

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
		} `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, "", err
	}
	var docs []Document
	for _, e := range tree.Tree {
		if e.Type != "blob" || !strings.HasSuffix(strings.ToLower(e.Path), ext) {
			continue
		}
		if pathPrefix != "" && !strings.HasPrefix(e.Path, pathPrefix) {
			continue
		}
		rawURL := fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", repo, branch, e.Path)
		content, err := fetchText(ctx, rawURL, token)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		docs = append(docs, Document{
			ExternalID: rawURL, Content: content, SourceRef: repo + "/" + e.Path,
			Metadata: map[string]any{"type": "doc", "title": e.Path, "repo": repo, "path": e.Path},
		})
		if len(docs) >= 500 { // safety cap per sync
			break
		}
	}
	if len(docs) == 0 {
		return nil, "", fmt.Errorf("github: no %s files under %q in %s@%s", ext, pathPrefix, repo, branch)
	}
	return docs, "", nil
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
