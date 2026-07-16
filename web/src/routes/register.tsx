import { useState } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { ShieldCheck } from "lucide-react";
import { AuthCard, AuthErrorAlert, Input, Label, Button, type AuthCardBrand } from "@togo-framework/ui";
import { auth, clearSession } from "../lib/auth";
import { APP_NAME } from "../lib/api";

const BRAND: AuthCardBrand = { name: APP_NAME, icon: <ShieldCheck className="h-10 w-10" />, tagline: { en: "Authentication & identity", ar: "المصادقة والهوية" } };

export function Register() {
  const nav = useNavigate();
  const [email, setEmail] = useState(""); const [password, setPassword] = useState("");
  const [err, setErr] = useState(""); const [busy, setBusy] = useState(false);
  async function submit(e: React.FormEvent) {
    e.preventDefault(); setBusy(true); setErr("");
    try { await auth.register(email, password); clearSession(); nav({ to: "/dashboard" }); }
    catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  }
  return (
    <AuthCard brand={BRAND} language="en" layout="split">
      <h1 className="text-2xl font-semibold">Create your account</h1>
      <p className="mt-1 text-sm text-muted-foreground">Get started in seconds</p>
      <form onSubmit={submit} className="mt-6 space-y-4">
        <AuthErrorAlert error={err} />
        <div className="space-y-1.5"><Label htmlFor="email">Email</Label><Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required /></div>
        <div className="space-y-1.5"><Label htmlFor="password">Password</Label><Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required minLength={8} /></div>
        <Button type="submit" className="w-full" disabled={busy}>{busy ? "Creating…" : "Create account"}</Button>
      </form>
      <p className="mt-6 text-center text-sm text-muted-foreground">Already registered? <Link to="/login" className="font-medium text-primary hover:underline">Sign in</Link></p>
    </AuthCard>
  );
}
