import { useCallback, useState } from "react";

// togo's <SidebarProvider> writes a `sidebar:state` cookie when toggled but never
// reads it back (it was built for SSR, where the cookie is read on the server).
// In this SPA that means the sidebar can get stuck collapsed to a bare icon rail
// with no labels. This hook makes the open/closed state CONTROLLED: it defaults to
// EXPANDED (full labeled nav) and restores/persists the user's choice across loads.
const COOKIE = "sidebar:state";
const MAX_AGE = 60 * 60 * 24 * 7; // 7 days

function readState(): boolean {
  if (typeof document === "undefined") return true;
  const m = document.cookie.match(/(?:^|;\s*)sidebar:state=(true|false)/);
  return m ? m[1] === "true" : true; // default: expanded
}

export function useSidebarState() {
  const [open, setOpenState] = useState<boolean>(readState);
  const setOpen = useCallback((v: boolean) => {
    setOpenState(v);
    document.cookie = `${COOKIE}=${v}; path=/; max-age=${MAX_AGE}`;
  }, []);
  return { open, setOpen };
}
