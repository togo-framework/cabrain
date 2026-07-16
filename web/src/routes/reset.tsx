import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { ShieldCheck } from "lucide-react";
import { AuthCard, AuthErrorAlert, Input, Label, Button, type AuthCardBrand } from "@togo-framework/ui";
import { auth } from "../lib/auth";
import { APP_NAME } from "../lib/api";

const BRAND: AuthCardBrand = { name: APP_NAME, icon: <ShieldCheck className="h-10 w-10" />, tagline: { en: "Reset your password", ar: "إعادة تعيين كلمة المرور" } };

export function Reset() {
  const [email, setEmail] = useState(""); const [sent, setSent] = useState(false);
  const [err, setErr] = useState(""); const [busy, setBusy] = useState(false);
  async function submit(e: React.FormEvent) {
    e.preventDefault(); setBusy(true); setErr("");
    try { await auth.requestOtp(email, "reset"); setSent(true); }
    catch (e: any) { setErr(e.message); } finally { setBusy(false); }
  }
  return (
    <AuthCard brand={BRAND} language="en" layout="split">
      <h1 className="text-2xl font-semibold">Reset password</h1>
      <p className="mt-1 text-sm text-muted-foreground">We'll email you a reset code</p>
      {sent ? (
        <p className="mt-6 rounded-lg border border-border bg-card p-4 text-sm text-muted-foreground">If an account exists for {email}, a reset code is on its way.</p>
      ) : (
        <form onSubmit={submit} className="mt-6 space-y-4">
          <AuthErrorAlert error={err} />
          <div className="space-y-1.5"><Label htmlFor="email">Email</Label><Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required /></div>
          <Button type="submit" className="w-full" disabled={busy}>{busy ? "Sending…" : "Send reset code"}</Button>
        </form>
      )}
      <p className="mt-6 text-center text-sm text-muted-foreground"><Link to="/login" className="font-medium text-primary hover:underline">Back to sign in</Link></p>
    </AuthCard>
  );
}
