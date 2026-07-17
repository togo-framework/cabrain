import { ReactNode, useCallback, useEffect, useState } from "react";
import { NeuralGlyph } from "../components/neural";
import { brainApi } from "../lib/brain";
import { sessionMe, type Me } from "../lib/auth";
import { LoginPage } from "./login-page";

// AuthContext exposes the signed-in identity (or null) to the console shell so it
// can render the "signed in as … / sign out" affordance. The gate itself decides
// whether to render the console or the login page.
type Phase = "loading" | "login" | "ready";

function Splash() {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background">
      <span className="flex h-12 w-12 animate-pulse items-center justify-center rounded-2xl bg-gradient-to-br from-indigo-500 via-violet-500 to-teal-400 text-white">
        <NeuralGlyph className="h-7 w-7" />
      </span>
    </div>
  );
}

// AuthGate resolves the backend enforcement flag (ping.authRequired) and the
// current session (/api/auth/me) once on load. When auth is enforced and there is
// no session it shows the login page; otherwise it renders the console. When
// enforcement is OFF (the default) it always renders the console so local/dev and
// the running instance stay reachable unauthenticated.
export function AuthGate({ children }: { children: ReactNode }) {
  const [phase, setPhase] = useState<Phase>("loading");

  const resolve = useCallback(async (force = false) => {
    let required = false;
    try { required = Boolean((await brainApi.ping()).authRequired); } catch { required = false; }
    if (!required) { setPhase("ready"); return; }
    const me = await sessionMe(force).catch(() => null);
    setPhase(me ? "ready" : "login");
  }, []);

  useEffect(() => { resolve(false); }, [resolve]);

  if (phase === "loading") return <Splash />;
  if (phase === "login") return <LoginPage onSignedIn={() => setPhase("ready")} />;
  return <>{children}</>;
}

// useSession is a light hook for the layout affordance. It reads the cached /me
// (populated during the gate) without forcing a network round-trip on every render.
export function useSession(): { me: Me | null; loading: boolean } {
  const [me, setMe] = useState<Me | null>(null);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    let alive = true;
    sessionMe().then((m) => { if (alive) { setMe(m); setLoading(false); } }).catch(() => { if (alive) setLoading(false); });
    return () => { alive = false; };
  }, []);
  return { me, loading };
}
