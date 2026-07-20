// togo auth client — talks to the auth plugin's /api/auth/* endpoints.
// Session is an HttpOnly cookie (sent same-origin automatically); CSRF uses the
// double-submit token. When a login also returns a JWT we cache it and attach it
// as `Authorization: Bearer` on brain API calls (belt-and-suspenders with the
// cookie, and the posture the auth Middleware accepts for either transport).
import type { AuthClient, LoginResult, OtpResult, Verify2FAResult } from "@togo-framework/ui";
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
  // no-store: the token rotates per request and each GET re-issues the cookie —
  // a cached response would hand back a token that no longer matches the cookie.
  const res = await fetch(`${API}/api/auth/csrf`, { credentials: "include", cache: "no-store" });
  const data = await res.json().catch(() => ({}));
  return data.csrf_token ?? "";
}

// One POST attempt: fetch a FRESH csrf token+cookie, then submit with the
// double-submit header. Each call re-issues the cookie, so a retry naturally
// recovers from a stale/rotated togo_csrf cookie.
async function postOnce(path: string, body?: unknown): Promise<Response> {
  const token = await csrf();
  return fetch(`${API}/api/auth/${path}`, {
    method: "POST",
    credentials: "include",
    // authHeaders() attaches the Bearer JWT when we hold one — authed POSTs
    // (e.g. change-password) work even if the session cookie is finicky, and the
    // server exempts Bearer requests from CSRF. Harmless on login (no token yet).
    headers: { "Content-Type": "application/json", "X-CSRF-Token": token, ...authHeaders() },
    body: body ? JSON.stringify(body) : undefined,
  });
}

async function post<T = any>(path: string, body?: unknown): Promise<T> {
  let res = await postOnce(path, body);
  // 403 here is almost always a stale/mismatched CSRF cookie (e.g. after a
  // backend restart or a cookie left over from an earlier session). Re-issue the
  // token+cookie and retry once before surfacing the error.
  if (res.status === 403) res = await postOnce(path, body);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw Object.assign(new Error(data.error || data.detail || `request failed (${res.status})`), { status: res.status });
  }
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
  // Change the signed-in user's password (requires an active session/token).
  changePassword: (oldPassword: string, newPassword: string) =>
    post("change-password", { old_password: oldPassword, new_password: newPassword }),
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

// --- togo auth UI adapter ----------------------------------------------------
// The @togo-framework/ui <AuthFlow> is UI-only: it drives an app-provided
// AuthClient (the "transport seam"). We map its methods onto the existing
// /api/auth/* transport above so the standard togo login UI works unchanged.
// `dev` gates the optional devLogin — LoginForm renders the "Continue as dev"
// button ONLY when the client exposes devLogin, so we attach it in dev only.
export function makeAuthClient(opts: { dev?: boolean } = {}): AuthClient {
  const client: AuthClient = {
    async login(email, password, rememberMe): Promise<LoginResult> {
      const d = await post<AuthResult & { challenge?: string; challenge_token?: string }>(
        "login", { email, password, remember_me: !!rememberMe },
      );
      if (d?.token) setToken(d.token);
      if (d?.challenge === "otp" || d?.challenge === "2fa") {
        return { challenge: d.challenge, challenge_token: d.challenge_token };
      }
      setStoredUser(d?.user ?? null); clearSession();
      return { challenge: "none" };
    },
    async sendOtp(email) { await post("otp", { email, purpose: "login" }); },
    async verifyOtp(email, code, challengeToken): Promise<OtpResult> {
      const d = await post<AuthResult & { challenge?: string; challenge_token?: string }>(
        "otp/verify", { email, code, challenge_token: challengeToken, purpose: "login" },
      );
      if (d?.token) setToken(d.token);
      if (d?.challenge === "2fa") return { challenge: "2fa", challenge_token: d.challenge_token };
      setStoredUser(d?.user ?? null); clearSession();
      return { challenge: "none" };
    },
    // CaBrain only offers password (+ dev) login — there is no mail service for
    // magic-link/OTP here. Returning just email_password stops <AuthFlow> from
    // rendering the "Send magic link" / "Email me a code" options.
    async getLoginMethods() { return { methods: ["email_password"] }; },
    async forgotPassword(email) { await post("otp", { email, purpose: "reset" }); },
    async resetPassword(token, newPassword) { await post("reset", { token, password: newPassword }); },
    async verify2FA(code, challengeToken): Promise<Verify2FAResult> {
      const d = await post<AuthResult>("2fa/verify", { code, challenge_token: challengeToken });
      if (d?.token) setToken(d.token);
      setStoredUser(d?.user ?? null); clearSession();
      return { challenge: "none" };
    },
  };
  if (opts.dev) {
    client.devLogin = async () => { await auth.devLogin(); };
  }
  return client;
}

// Session cache so the router's guards resolve /me once per navigation pass
// instead of re-fetching on every route. Clear it after login/logout/register.
let _meCache: Promise<Me | null> | null = null;
export function sessionMe(force = false): Promise<Me | null> {
  if (force || !_meCache) _meCache = auth.me();
  return _meCache;
}
export function clearSession() { _meCache = null; }
