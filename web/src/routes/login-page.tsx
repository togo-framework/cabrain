import { useEffect, useMemo, useState } from "react";
import { AuthFlow } from "@togo-framework/ui";
import { NeuralGlyph, SynapseField } from "../components/neural";
import { auth, clearSession, makeAuthClient } from "../lib/auth";

// Full-screen login gate for the CaBrain console. Rendered by <AuthGate> when the
// backend enforces auth (CABRAIN_REQUIRE_AUTH) and there is no active session.
// Uses the togo auth UI (@togo-framework/ui <AuthFlow>) driven by our AuthClient
// adapter (see lib/auth.ts). Keeps the neural aesthetic: synapse-field backdrop
// behind togo's centered auth card, and the violet→teal glyph as the brand crest.
export function LoginPage({ onSignedIn }: { onSignedIn: () => void }) {
  // Only advertise the developer login when the backend actually exposes it
  // (auth-dev is disabled in production) so the button never lies. LoginForm shows
  // "Continue as dev" iff the client exposes devLogin, so gate it on this.
  const [devAvailable, setDevAvailable] = useState(false);
  useEffect(() => {
    let alive = true;
    auth.methods()
      .then((ms) => { if (alive) setDevAvailable(ms.some((m) => m.type === "dev")); })
      .catch(() => { /* fail closed: no dev button */ });
    return () => { alive = false; };
  }, []);

  const client = useMemo(() => makeAuthClient({ dev: devAvailable }), [devAvailable]);

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-background p-4">
      <SynapseField className="pointer-events-none absolute inset-0 opacity-40" />
      <div className="absolute inset-0 bg-gradient-to-b from-transparent via-transparent to-background" />

      <div className="relative z-10 w-full">
        <AuthFlow
          authClient={client}
          layout="centered"
          onLanguageToggle={null}
          onSuccess={() => { clearSession(); onSignedIn(); }}
          brand={{
            icon: <NeuralGlyph className="h-8 w-8" />,
            name: "CaBrain console",
            tagline: {
              en: "Sign in to access the memory organ",
              ar: "سجّل الدخول للوصول إلى عضو الذاكرة",
            },
          }}
        />
      </div>
    </div>
  );
}
