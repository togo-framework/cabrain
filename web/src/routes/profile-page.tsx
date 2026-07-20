import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { ArrowLeft, Loader2 } from "lucide-react";
import {
  ProfileView,
  Button, Input, Label,
  Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter,
} from "@togo-framework/ui";
import { useSession } from "./auth-gate";
import { auth } from "../lib/auth";

// Change-password dialog — the one profile action wired to a real endpoint
// (POST /api/auth/change-password). The others (avatar, 2FA, session revoke) have
// no backend yet, so ProfileView renders without those callbacks.
function ChangePasswordDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [oldPw, setOldPw] = useState("");
  const [newPw, setNewPw] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [done, setDone] = useState(false);

  const reset = () => { setOldPw(""); setNewPw(""); setConfirm(""); setError(""); setDone(false); setBusy(false); };
  const close = () => { reset(); onClose(); };

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError("");
    if (newPw !== confirm) { setError("New passwords do not match."); return; }
    setBusy(true);
    try {
      await auth.changePassword(oldPw, newPw);
      setDone(true);
      setTimeout(close, 1200);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not change password.");
    } finally { setBusy(false); }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && close()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Change password</DialogTitle>
          <DialogDescription>Enter your current password and choose a new one.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="cur">Current password</Label>
            <Input id="cur" type="password" autoComplete="current-password" value={oldPw} onChange={(e) => setOldPw(e.target.value)} required />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="new">New password</Label>
            <Input id="new" type="password" autoComplete="new-password" value={newPw} onChange={(e) => setNewPw(e.target.value)} required />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="conf">Confirm new password</Label>
            <Input id="conf" type="password" autoComplete="new-password" value={confirm} onChange={(e) => setConfirm(e.target.value)} required />
          </div>
          {error && (
            <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</p>
          )}
          {done && (
            <p className="rounded-md border border-emerald-500/40 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-500">Password updated.</p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={close}>Cancel</Button>
            <Button type="submit" disabled={busy || done}>
              {busy ? <Loader2 className="h-4 w-4 animate-spin" /> : null} Update password
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function ProfilePage() {
  const { me, loading } = useSession();
  const [pwOpen, setPwOpen] = useState(false);

  return (
    <div className="mx-auto w-full max-w-3xl space-y-5 p-4 sm:p-6">
      <Link to="/" className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="h-4 w-4" /> Back to brains
      </Link>

      {loading ? (
        <p className="text-sm text-muted-foreground">Loading your profile…</p>
      ) : !me ? (
        <p className="text-sm text-muted-foreground">You are not signed in.</p>
      ) : (
        <ProfileView
          user={{
            email: me.email,
            name: me.email?.split("@")[0],
            roles: Array.isArray(me.roles) ? me.roles.filter(Boolean) : [],
          }}
          onChangePassword={() => setPwOpen(true)}
        />
      )}

      <ChangePasswordDialog open={pwOpen} onClose={() => setPwOpen(false)} />
    </div>
  );
}
