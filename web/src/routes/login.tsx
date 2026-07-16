import { useState, useEffect } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { ShieldCheck, Terminal } from "lucide-react";
import { AuthCard, AuthErrorAlert, Input, Label, Button, type AuthCardBrand } from "@togo-framework/ui";
import { auth, clearSession } from "../lib/auth";
import { API, APP_NAME } from "../lib/api";

const BRAND: AuthCardBrand = { name: APP_NAME, icon: <ShieldCheck className="h-10 w-10" />, tagline: { en: "Authentication & identity", ar: "المصادقة والهوية" } };

function Methods() {
  const [methods, setMethods] = useState<{ name: string; label: string; type: string; url: string }[]>([]);
  useEffect(() => { auth.methods().then(setMethods); }, []);
  if (!methods.length) return null;
  return (
    <div className="mt-4 space-y-2">
      <div className="my-4 flex items-center gap-3 text-xs text-muted-foreground"><span className="h-px flex-1 bg-border" />or<span className="h-px flex-1 bg-border" /></div>
      {methods.map((m) => (
        <Button key={m.name} variant="outline" className="w-full" onClick={async () => {
          if (m.type === "dev") { await fetch(`${API}${m.url}`, { method: "POST", credentials: "include" }); window.location.href = "/dashboard"; }
          else window.location.href = `${API}${m.url}`;
        }}>{m.type === "dev" ? <Terminal className="h-4 w-4" /> : null}{m.label}</Button>
      ))}
    </div>
  );
}

export function Login() {
  const nav = useNavigate();
  const [email, setEmail] = useState(""); const [password, setPassword] = useState("");
  const [err, setErr] = useState(""); const [busy, setBusy] = useState(false);
  async function submit(e: React.FormEvent) {
    e.preventDefault(); setBusy(true); setErr("");
    try { await auth.login(email, password); clearSession(); nav({ to: "/dashboard" }); }
    catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  }
  return (
    <AuthCard brand={BRAND} language="en" layout="split">
      <h1 className="text-2xl font-semibold">Welcome back</h1>
      <p className="mt-1 text-sm text-muted-foreground">Sign in to your account</p>
      <form onSubmit={submit} className="mt-6 space-y-4">
        <AuthErrorAlert error={err} />
        <div className="space-y-1.5"><Label htmlFor="email">Email</Label><Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required /></div>
        <div className="space-y-1.5"><Label htmlFor="password">Password</Label><Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required /></div>
        <Button type="submit" className="w-full" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</Button>
      </form>
      <Methods />
      <p className="mt-6 text-center text-sm text-muted-foreground">No account? <Link to="/register" className="font-medium text-primary hover:underline">Create one</Link></p>
    </AuthCard>
  );
}
