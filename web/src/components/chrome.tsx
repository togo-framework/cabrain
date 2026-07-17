import { LogOut } from "lucide-react";
import { Button } from "@togo-framework/ui";
import { useSession } from "../routes/auth-gate";
import { auth } from "../lib/auth";

/** Signed-in identity + a sign-out control. Only appears when there is an active
 * session (auth may be off, in which case there's nothing to show). Sign out
 * clears the session then reloads so the AuthGate re-evaluates. Shared by the hub,
 * workspace, and admin chrome. */
export function UserMenu() {
  const { me } = useSession();
  if (!me) return null;
  const label = me.email || (Array.isArray(me.roles) && me.roles[0]) || "signed in";
  return (
    <div className="flex items-center gap-2">
      <span className="hidden text-xs text-muted-foreground sm:inline">
        Signed in as <span className="font-medium text-foreground">{label}</span>
      </span>
      <Button variant="ghost" size="sm" title="Sign out"
        onClick={async () => { await auth.logout(); window.location.reload(); }}>
        <LogOut className="h-4 w-4" />
        <span className="hidden sm:inline">Sign out</span>
      </Button>
    </div>
  );
}
