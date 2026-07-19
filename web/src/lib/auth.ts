// togo auth client — talks to the auth plugin's /api/auth/* endpoints.
// Session is an HttpOnly cookie (sent same-origin automatically); CSRF uses the
// double-submit token. When a login also returns a JWT we cache it and attach it
// as `Authorization: Bearer` on brain API calls (belt-and-suspenders with the
// cookie, and the posture the auth Middleware accepts for either transport).
import { API } from "./api";

// --- Bearer token store (JWT from a credential login; dev-login is cookie-only) ---
const TOKEN_KEY = "cabrain.auth.token";
export function getToken(): string {
  try { return localStorage.getItem(TOKEN_KEY) ?? ""; } catch { return ""; }
}
export function setToken(t: string) {
  try { t ? localStorage.setItem(TOKEN_KEY, t) : localStorage.removeItem(TOKEN_KEY); } catch { /* ignore */ }
}
export function clearToken() { setToken(""); }
/** Authorization header for brain API calls, or {} when signed in via cookie only. */
export function authHeaders(): Record<string, string> {
  const t = getToken();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

// --- Cached identity ---------------------------------------------------------
// The login response is authoritative for the signed-in user. We persist it so a
// page refresh can re-hydrate the session WITHOUT a follow-up /api/auth/me call
// (the auth plugin doesn't expose one — an unregistered /api/* used to fall through
// to the SPA shell and crash the parser with "Unexpected token '<'").
const USER_KEY = "cabrain.auth.user";
export function getStoredUser(): Me | null {
  try { const s = localStorage.getItem(USER_KEY); return s ? (JSON.parse(s) as Me) : null; } catch { return null; }
}
export function setStoredUser(u: Me | null) {
  try { u ? localStorage.setItem(USER_KEY, JSON.stringify(u)) : localStorage.removeItem(USER_KEY); } catch { /* ignore */ }
}

async function csrf(): Promise<string> {
  const res = await fetch(`${API}/api/auth/csrf`, { credentials: "include" });
  const data = await res.json().catch(() => ({}));
  return data.csrf_token ?? "";
}

async function post<T = any>(path: string, body?: unknown): Promise<T> {
  const token = await csrf();
  const res = await fetch(`${API}/api/auth/${path}`, {
    method: "POST",
    credentials: "include",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": token },
    body: body ? JSON.stringify(body) : undefined,
  });
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || data.detail || `request failed (${res.status})`);
  return data as T;
}

export interface Me { email: string; roles?: string[]; permissions?: string[]; [k: string]: unknown }

// The credential login/register response carries the authoritative user object.
type AuthResult = { token?: string; user?: Me };

export const auth = {
  login: async (email: string, password: string): Promise<AuthResult> => {
    const d = await post<AuthResult>("login", { email, password });
    if (d?.token) setToken(d.token);
    setStoredUser(d?.user ?? null);
    clearSession();
    return d;
  },
  register: async (email: string, password: string): Promise<AuthResult> => {
    const d = await post<AuthResult>("register", { email, password });
    if (d?.token) setToken(d.token);
    setStoredUser(d?.user ?? null);
    clearSession();
    return d;
  },
  // One-click developer login (auth-dev). Cookie-only session (no body token);
  // the route is not CSRF-guarded. Disabled server-side in production.
  devLogin: async (): Promise<AuthResult> => {
    const res = await fetch(`${API}/api/auth/dev/login`, { method: "POST", credentials: "include" });
    if (!res.ok) throw new Error(`dev login failed (${res.status})`);
    clearToken();
    const d = (await res.json().catch(() => ({}))) as AuthResult;
    if (d?.token) setToken(d.token);     // some dev drivers return a JWT too
    setStoredUser(d?.user ?? null);
    clearSession();
    return d;
  },
  logout: async () => {
    try { await post("logout"); } finally { clearToken(); setStoredUser(null); clearSession(); }
  },
  // Resolve the current identity for page-load hydration. Prefer the server's
  // /api/auth/me when it exists and answers JSON; otherwise fall back to the user
  // persisted at login (so a refresh keeps the session while a valid token is held).
  // Never JSON-parse an HTML response — that was the original crash.
  me: async (): Promise<Me | null> => {
    try {
      const res = await fetch(`${API}/api/auth/me`, { credentials: "include", headers: authHeaders() });
      const ct = res.headers.get("content-type") || "";
      if (res.ok && ct.includes("application/json")) {
        const u = (await res.json().catch(() => null)) as Me | null;
        if (u && u.email) { setStoredUser(u); return u; }
      }
    } catch { /* fall through to the stored identity */ }
    // No usable /me endpoint — trust the persisted login as long as a token is held.
    return getToken() ? getStoredUser() : null;
  },
  methods: async (): Promise<{ name: string; label: string; type: string; url: string }[]> => {
    const res = await fetch(`${API}/api/auth/methods`, { credentials: "include" }).catch(() => null);
    if (!res || !res.ok) return [];
    const d = await res.json().catch(() => ({ methods: [] }));
    return d.methods ?? [];
  },
  requestOtp: (email: string, purpose = "reset") => post("otp", { email, purpose }),
  verifyOtp: (email: string, code: string, purpose = "reset") => post("otp/verify", { email, code, purpose }),
};

// Session cache so the router's guards resolve /me once per navigation pass
// instead of re-fetching on every route. Clear it after login/logout/register.
let _meCache: Promise<Me | null> | null = null;
export function sessionMe(force = false): Promise<Me | null> {
  if (force || !_meCache) _meCache = auth.me();
  return _meCache;
}
export function clearSession() { _meCache = null; }
