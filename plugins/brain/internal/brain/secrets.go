package brain

// Per-brain secrets vault (namespace-scoped, encrypted at rest, ACL-gated).
//
// Why it exists: retained content (a session, a .env dump, a chat log) often
// carries live secrets — API keys, passwords, connection strings, private keys.
// Storing those verbatim in `memories.content` would (a) embed them into the
// vector index and (b) leak them to anyone who can recall. Instead, on retain we
// DETECT secrets, move each value into this vault (AES-256-GCM), and REDACT the
// content to a `[secret:<name>]` reference. The memory stays useful and
// searchable; the raw value lives only in the vault and is revealed only to a
// caller with write/admin on that brain. Secrets persist across sessions.
//
// Storage lives in the `cabrain_auth` schema (created + isolated for this app),
// so `secrets` resolves there via search_path and never collides with anything
// in `public`.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

// --- encryption ---------------------------------------------------------------

// secretKey resolves the 32-byte AES key. Preference: CABRAIN_SECRETS_KEY (64 hex
// chars) → derived from AUTH_SECRET → error (fail closed; never store plaintext).
func secretKey() ([]byte, error) {
	if h := strings.TrimSpace(os.Getenv("CABRAIN_SECRETS_KEY")); h != "" {
		b, err := hex.DecodeString(h)
		if err == nil && len(b) == 32 {
			return b, nil
		}
		return nil, errors.New("CABRAIN_SECRETS_KEY must be 64 hex chars (32 bytes)")
	}
	if s := strings.TrimSpace(os.Getenv("AUTH_SECRET")); s != "" {
		sum := sha256.Sum256([]byte("cabrain-secrets-v1:" + s))
		return sum[:], nil
	}
	return nil, errors.New("secrets vault has no key: set CABRAIN_SECRETS_KEY (64 hex) or AUTH_SECRET")
}

// encryptSecret returns nonce||ciphertext (GCM), safe to store as bytea.
func encryptSecret(plaintext string) ([]byte, error) {
	key, err := secretKey()
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

func decryptSecret(enc []byte) (string, error) {
	key, err := secretKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(enc) < gcm.NonceSize() {
		return "", errors.New("secret ciphertext too short")
	}
	nonce, ct := enc[:gcm.NonceSize()], enc[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", errors.New("secret decrypt failed (wrong key?)")
	}
	return string(pt), nil
}

// maskHint returns a non-reversible preview for listings, e.g. "sk-…a1b2" or "••••".
func maskHint(v string) string {
	v = strings.TrimSpace(v)
	if len(v) <= 4 {
		return "••••"
	}
	head := ""
	if i := strings.IndexAny(v, "-_"); i > 0 && i <= 6 {
		head = v[:i+1]
	}
	return head + "…" + v[len(v)-4:]
}

// --- detection ----------------------------------------------------------------

type detectedSecret struct {
	Name  string
	Value string
	Kind  string
}

// secretMatchers are ordered most-specific-first. Each returns the secret value
// (submatch group `val` or whole match) and a stable name basis.
var secretMatchers = []struct {
	kind string
	re   *regexp.Regexp
	// nameFrom, if set, derives the vault key from a submatch (e.g. the env var name).
	nameGroup int
	valGroup  int
}{
	{kind: "private_key", re: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`), valGroup: 0},
	{kind: "connection_string", re: regexp.MustCompile(`(?i)\b(?:postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqps?)://[^\s:@/]+:[^\s@]+@[^\s"'` + "`" + `]+`), valGroup: 0},
	{kind: "aws_key", re: regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), valGroup: 0},
	{kind: "openai_key", re: regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{20,}\b`), valGroup: 0},
	{kind: "pat", re: regexp.MustCompile(`\b(?:os_pat_|ghp_|gho_|github_pat_|xoxb-|xoxp-|glpat-)[A-Za-z0-9_\-]{10,}\b`), valGroup: 0},
	{kind: "jwt", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\b`), valGroup: 0},
	{kind: "bearer", re: regexp.MustCompile(`(?i)\bbearer\s+([A-Za-z0-9._\-]{20,})`), valGroup: 1},
	{kind: "env", re: regexp.MustCompile(`(?m)^\s*([A-Z][A-Z0-9_]{2,})\s*=\s*['"]?([^\s'"]{6,})['"]?\s*$`), nameGroup: 1, valGroup: 2},
	{kind: "credential", re: regexp.MustCompile(`(?i)\b(pass(?:word|wd)?|secret|api[_-]?key|access[_-]?token|auth[_-]?token)\b\s*[:=]\s*['"]?([^\s'"]{6,})['"]?`), nameGroup: 1, valGroup: 2},
}

// looksLikePlaceholder skips obvious non-secrets (examples, redaction markers).
func looksLikePlaceholder(v string) bool {
	lv := strings.ToLower(v)
	for _, p := range []string{"example", "changeme", "your_", "xxxx", "****", "…", "redacted", "<", "placeholder", "dummy"} {
		if strings.Contains(lv, p) {
			return true
		}
	}
	return false
}

// extractSecrets scans content, returns the redacted content (secret values
// replaced with `[secret:<name>]`) and the list of detected secrets (deduped by
// value). Names are derived from the env var / credential label when available,
// else "<kind>-<8hex of value>".
func extractSecrets(content string) (string, []detectedSecret) {
	redacted := content
	seen := map[string]string{} // value -> name (dedup)
	var out []detectedSecret

	for _, m := range secretMatchers {
		for _, loc := range m.re.FindAllStringSubmatch(content, -1) {
			val := loc[m.valGroup]
			val = strings.TrimSpace(val)
			if len(val) < 6 || looksLikePlaceholder(val) {
				continue
			}
			name, dup := seen[val]
			if !dup {
				if m.nameGroup > 0 && m.nameGroup < len(loc) && loc[m.nameGroup] != "" {
					name = sanitizeSecretName(loc[m.nameGroup])
				} else {
					sum := sha256.Sum256([]byte(val))
					name = fmt.Sprintf("%s-%s", m.kind, hex.EncodeToString(sum[:4]))
				}
				seen[val] = name
				out = append(out, detectedSecret{Name: name, Value: val, Kind: m.kind})
			}
			redacted = strings.ReplaceAll(redacted, val, "[secret:"+name+"]")
		}
	}
	return redacted, out
}

var nameSanitizer = regexp.MustCompile(`[^A-Za-z0-9_.\-]+`)

func sanitizeSecretName(s string) string {
	s = nameSanitizer.ReplaceAllString(strings.TrimSpace(s), "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "secret"
	}
	if len(s) > 96 {
		s = s[:96]
	}
	return s
}

// --- store --------------------------------------------------------------------

// SecretMeta is the listing view — never carries the value.
type SecretMeta struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Hint      string `json:"hint"`
	Kind      string `json:"kind"`
	SourceRef string `json:"sourceRef,omitempty"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// PutSecret upserts an encrypted secret under (namespace, name).
func (s *Store) PutSecret(ctx context.Context, ns, name, value, kind, sourceRef, by string) error {
	if ns == "" || name == "" {
		return errors.New("namespace and name required")
	}
	if value == "" {
		return errors.New("empty secret value")
	}
	enc, err := encryptSecret(value)
	if err != nil {
		return err
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO secrets (namespace, name, value_enc, hint, kind, source_ref, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7, now(), now())
		ON CONFLICT (namespace, name) DO UPDATE
		  SET value_enc = EXCLUDED.value_enc, hint = EXCLUDED.hint, kind = EXCLUDED.kind,
		      source_ref = COALESCE(EXCLUDED.source_ref, secrets.source_ref), updated_at = now()`,
		ns, name, enc, maskHint(value), nullStr(kind), nullStr(sourceRef), nullStr(by))
	return err
}

// ListSecrets returns metadata (no values) for a brain.
func (s *Store) ListSecrets(ctx context.Context, ns string) ([]SecretMeta, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `
		SELECT namespace, name, COALESCE(hint,''), COALESCE(kind,''), COALESCE(source_ref,''),
		       COALESCE(created_by,''), created_at, updated_at
		FROM secrets WHERE namespace = $1 ORDER BY name`, ns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SecretMeta
	for rows.Next() {
		var m SecretMeta
		var created, updated time.Time
		if err := rows.Scan(&m.Namespace, &m.Name, &m.Hint, &m.Kind, &m.SourceRef, &m.CreatedBy, &created, &updated); err != nil {
			return nil, err
		}
		m.CreatedAt = created.UTC().Format(time.RFC3339)
		m.UpdatedAt = updated.UTC().Format(time.RFC3339)
		out = append(out, m)
	}
	return out, rows.Err()
}

// RevealSecret decrypts and returns the value for (namespace, name). Caller ACL is
// enforced at the handler; this is the raw read.
func (s *Store) RevealSecret(ctx context.Context, ns, name string) (string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return "", err
	}
	var enc []byte
	err = db.QueryRowContext(ctx, `SELECT value_enc FROM secrets WHERE namespace = $1 AND name = $2`, ns, name).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return decryptSecret(enc)
}

// DeleteSecret removes a secret. Returns whether a row was deleted.
func (s *Store) DeleteSecret(ctx context.Context, ns, name string) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	res, err := db.ExecContext(ctx, `DELETE FROM secrets WHERE namespace = $1 AND name = $2`, ns, name)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// captureSecrets is the retain-path hook: it extracts secrets from content, stores
// each in the vault, and returns the redacted content plus the captured names.
// Best-effort — a vault error must never fail the retain; on error it returns the
// ORIGINAL content unchanged (so nothing is silently dropped) and no names.
func (s *Store) captureSecrets(ctx context.Context, ns, content, sourceRef, by string) (string, []string) {
	redacted, found := extractSecrets(content)
	if len(found) == 0 {
		return content, nil
	}
	var names []string
	for _, d := range found {
		if err := s.PutSecret(ctx, ns, d.Name, d.Value, d.Kind, sourceRef, by); err != nil {
			// Could not vault it (e.g. no key) — do not redact that value; keep content intact.
			return content, nil
		}
		names = append(names, d.Name)
	}
	return redacted, names
}
