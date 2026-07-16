// togo auth client — talks to the auth plugin's /api/auth/* endpoints.
// Session is an HttpOnly cookie; CSRF uses the double-submit token.
import { API } from "./api";

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
  login: (email: string, password: string) => post("login", { email, password }),
  register: (email: string, password: string) => post("register", { email, password }),
  logout: () => post("logout"),
  me: async (): Promise<Me | null> => {
    const res = await fetch(`${API}/api/auth/me`, { credentials: "include" });
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

// Session cache so the router's beforeLoad guards resolve /me once per navigation
// pass instead of re-fetching on every route. Clear it after login/logout/register.
let _meCache: Promise<Me | null> | null = null;
export function sessionMe(force = false): Promise<Me | null> {
  if (force || !_meCache) _meCache = auth.me();
  return _meCache;
}
export function clearSession() { _meCache = null; }
