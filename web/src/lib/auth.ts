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

export const auth = {
  login: async (email: string, password: string) => {
    const d = await post<{ token?: string }>("login", { email, password });
    if (d?.token) setToken(d.token);
    clearSession();
    return d;
  },
  register: async (email: string, password: string) => {
    const d = await post<{ token?: string }>("register", { email, password });
    if (d?.token) setToken(d.token);
    clearSession();
    return d;
  },
  // One-click developer login (auth-dev). Cookie-only session (no body token);
  // the route is not CSRF-guarded. Disabled server-side in production.
  devLogin: async () => {
    const res = await fetch(`${API}/api/auth/dev/login`, { method: "POST", credentials: "include" });
    if (!res.ok) throw new Error(`dev login failed (${res.status})`);
    clearToken();
    clearSession();
    return res.json().catch(() => ({}));
  },
  logout: async () => {
    try { await post("logout"); } finally { clearToken(); clearSession(); }
  },
  me: async (): Promise<Me | null> => {
    const res = await fetch(`${API}/api/auth/me`, { credentials: "include", headers: authHeaders() });
    if (!res.ok) return null;
    return res.json();
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
