import { LogOut, User as UserIcon, ChevronDown } from "lucide-react";
import { Link } from "@tanstack/react-router";
import {
  Button,
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent,
  DropdownMenuItem, DropdownMenuSeparator, DropdownMenuLabel,
} from "@togo-framework/ui";
import { useSession } from "../routes/auth-gate";
import { auth } from "../lib/auth";

/** First-letter monogram for the signed-in user — matches the brain avatars'
 *  restrained, one-accent identity style. */
function Avatar({ seed }: { seed: string }) {
  const ch = (seed.trim()[0] || "?").toUpperCase();
  return (
    <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-gradient-to-br from-indigo-500 via-violet-500 to-teal-400 text-xs font-semibold text-white">
      {ch}
    </span>
  );
}

/** Signed-in identity as an account dropdown: avatar + email trigger opening a menu
 * with the profile page and sign-out. Only renders with an active session (auth may
 * be off, in which case there is nothing to show). Sign out clears the session then
 * reloads so the AuthGate re-evaluates. Shared by the hub, workspace, and admin chrome. */
export function UserMenu() {
  const { me } = useSession();
  if (!me) return null;
  const email = me.email || "signed in";
  const roles = Array.isArray(me.roles) ? me.roles.filter(Boolean) : [];

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="sm" className="gap-2 px-1.5 sm:px-2" aria-label="Account menu">
          <Avatar seed={email} />
          <span className="hidden max-w-[160px] truncate text-sm sm:inline">{email}</span>
          <ChevronDown className="hidden h-4 w-4 text-muted-foreground sm:inline" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <DropdownMenuLabel className="flex items-center gap-2.5 py-2">
          <Avatar seed={email} />
          <span className="min-w-0">
            <span className="block truncate text-sm font-medium text-foreground">{email}</span>
            <span className="block truncate text-xs font-normal text-muted-foreground">
              {roles.length ? roles.join(" · ") : "member"}
            </span>
          </span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link to="/profile"><UserIcon className="h-4 w-4" /> Profile &amp; settings</Link>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          className="text-destructive focus:text-destructive"
          onSelect={async () => { await auth.logout(); window.location.reload(); }}
        >
          <LogOut className="h-4 w-4" /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
