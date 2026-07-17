import { useEffect, useState } from "react";
import { Button, Input, Label } from "@togo-framework/ui";
import { NeuralGlyph, SynapseField } from "../components/neural";
import { auth, type Me } from "../lib/auth";

// Full-screen login gate for the CaBrain console. Rendered by <AuthGate> when the
// backend enforces auth (CABRAIN_REQUIRE_AUTH) and there is no active session.
// Keeps the neural aesthetic: synapse field backdrop + the violet→teal glyph.
export function LoginPage({ onSignedIn }: { onSignedIn: (me: Me) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [devAvailable, setDevAvailable] = useState(false);

  // Only advertise the developer login when the backend actually exposes it
  // (auth-dev is disabled in production), so the button never lies.
  useEffect(() => {
    let alive = true;
    auth.methods().then((ms) => {
      if (alive) setDevAvailable(ms.some((m) => m.type === "dev"));
    });
    return () => { alive = false; };
  }, []);

  async function finish() {
    const me = await auth.me();
    if (me) { onSignedIn(me); return; }
    setError("Signed in, but the session did not resolve. Check AUTH_SECRET / cookies.");
  }

  async function onCredentials(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true); setError("");
    try {
      await auth.login(email.trim(), password);
      await finish();
    } catch (err) {
      setError(err instanceof Error ? err.message : "login failed");
    } finally { setBusy(false); }
  }

  async function onDevLogin() {
    setBusy(true); setError("");
    try {
      await auth.devLogin();
      await finish();
    } catch (err) {
      setError(err instanceof Error ? err.message : "developer login failed");
    } finally { setBusy(false); }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-background p-4">
      <SynapseField className="pointer-events-none absolute inset-0 opacity-40" />
      <div className="absolute inset-0 bg-gradient-to-b from-transparent via-transparent to-background" />

      <div className="relative z-10 w-full max-w-sm rounded-2xl border border-border bg-card/80 p-8 shadow-2xl backdrop-blur-xl">
        <div className="mb-6 flex flex-col items-center text-center">
          <span className="mb-3 flex h-14 w-14 items-center justify-center rounded-2xl bg-gradient-to-br from-indigo-500 via-violet-500 to-teal-400 text-white shadow-[0_0_28px_-6px] shadow-violet-500/60">
            <NeuralGlyph className="h-8 w-8" />
          </span>
          <h1 className="text-lg font-semibold">CaBrain console</h1>
          <p className="mt-1 text-sm text-muted-foreground">Sign in to access the memory organ</p>
        </div>

        <form onSubmit={onCredentials} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="email">Email</Label>
            <Input id="email" type="email" autoComplete="username" placeholder="you@studio.dev"
              value={email} onChange={(e) => setEmail(e.target.value)} required />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input id="password" type="password" autoComplete="current-password" placeholder="••••••••"
              value={password} onChange={(e) => setPassword(e.target.value)} required />
          </div>

          {error && (
            <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </p>
          )}

          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>

        {devAvailable && (
          <>
            <div className="my-4 flex items-center gap-3 text-xs text-muted-foreground">
              <span className="h-px flex-1 bg-border" />
              <span>or</span>
              <span className="h-px flex-1 bg-border" />
            </div>
            <Button type="button" variant="outline" className="w-full" disabled={busy} onClick={onDevLogin}>
              Login as developer
            </Button>
            <p className="mt-2 text-center text-xs text-muted-foreground">
              Dev login is disabled in production.
            </p>
          </>
        )}
      </div>
    </div>
  );
}
