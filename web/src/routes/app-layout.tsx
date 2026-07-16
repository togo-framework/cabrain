import { useEffect, useState } from "react";
import { Outlet, useNavigate, useRouterState, Link } from "@tanstack/react-router";
import { LayoutGrid, Table2, User, LogOut, Layers, ChevronDown } from "lucide-react";
import {
  SidebarProvider, Sidebar, SidebarHeader, SidebarContent,
  SidebarGroup, SidebarGroupLabel, SidebarMenu, SidebarMenuItem, SidebarMenuButton,
  SidebarInset, SidebarTrigger, Avatar, AvatarFallback,
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator,
  StatusBadge, ThemePicker, useT,
} from "@togo-framework/ui";
import { auth, sessionMe, clearSession, type Me } from "../lib/auth";
import { metaResources, adminList, type ResourceMeta } from "../lib/admin";
import { ToastProvider } from "../components/admin/toast";
import { API, APP_NAME } from "../lib/api";

/** Group a flat resource list by the optional `group` field.
 * Resources with no group fall into the "Resources" default. */
function groupResources(resources: ResourceMeta[]): Map<string, ResourceMeta[]> {
  const map = new Map<string, ResourceMeta[]>();
  for (const r of resources) {
    const g = r.group ?? "Resources";
    if (!map.has(g)) map.set(g, []);
    map.get(g)!.push(r);
  }
  return map;
}

const ar_label = (en: string, ar: string, isAr: boolean) => isAr ? ar : en;

export function AppLayout() {
  const nav = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const { language } = useT();
  const [me, setMe] = useState<Me | null>(null);
  const [resources, setResources] = useState<ResourceMeta[]>([]);
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [live, setLive] = useState(false);
  const ar = language === "ar";

  useEffect(() => {
    // Auth is already guaranteed by the route's beforeLoad guard — just read the cached user.
    sessionMe().then(setMe);
    metaResources().then((rs) => {
      setResources(rs);
      // Sidebar count badges — one fetch per resource (best-effort).
      rs.forEach((r) => adminList(r.table).then((rows) => setCounts((c) => ({ ...c, [r.table]: rows.length }))).catch(() => {}));
    });
    const es = new EventSource(`${API}/events`);
    es.onopen = () => setLive(true);
    es.onerror = () => setLive(false);
    return () => es.close();
  }, []);

  const initial = (me?.email ?? "?").charAt(0).toUpperCase();
  const go = (to: string) => nav({ to });
  const grouped = groupResources(resources);

  return (
    <ToastProvider dir={ar ? "rtl" : "ltr"}>
    <SidebarProvider dir={ar ? "rtl" : "ltr"}>
      {/* collapsible="icon" → the SidebarTrigger minimizes the sidebar to icons. */}
      <Sidebar collapsible="icon" side={ar ? "right" : "left"}>
        <SidebarHeader>
          <Link to="/dashboard" className="flex items-center gap-2 px-2 py-1.5">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground"><Layers className="h-4 w-4" /></span>
            <span className="truncate font-semibold group-data-[collapsible=icon]:hidden">{APP_NAME}</span>
          </Link>
        </SidebarHeader>
        <SidebarContent>
          {/* Core nav — always visible */}
          <SidebarGroup>
            <SidebarMenu>
              <SidebarMenuItem>
                <SidebarMenuButton isActive={pathname === "/dashboard"} tooltip={ar_label("Dashboard", "لوحة التحكم", ar)} onClick={() => go("/dashboard")}>
                  <LayoutGrid className="h-4 w-4" /><span>{ar_label("Dashboard", "لوحة التحكم", ar)}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
              <SidebarMenuItem>
                <SidebarMenuButton isActive={pathname === "/admin"} tooltip={ar_label("Admin", "الإدارة", ar)} onClick={() => go("/admin")}>
                  <Table2 className="h-4 w-4" /><span>{ar_label("Admin", "الإدارة", ar)}</span>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroup>

          {/* Resource groups — each `group` value becomes its own sidebar section */}
          {Array.from(grouped.entries()).map(([groupName, groupResources]) => (
            <SidebarGroup key={groupName}>
              <SidebarGroupLabel>{groupName}</SidebarGroupLabel>
              <SidebarMenu>
                {groupResources.map((r) => (
                  <SidebarMenuItem key={r.table}>
                    <SidebarMenuButton
                      isActive={pathname === `/admin/${r.table}`}
                      tooltip={r.name || r.table}
                      onClick={() => go(`/admin/${r.table}`)}
                    >
                      <Table2 className="h-4 w-4" />
                      <span className="capitalize">{r.name || r.table}</span>
                      {counts[r.table] !== undefined && (
                        <span className="ms-auto rounded-full bg-muted px-1.5 text-xs text-muted-foreground group-data-[collapsible=icon]:hidden">
                          {counts[r.table]}
                        </span>
                      )}
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroup>
          ))}
        </SidebarContent>
      </Sidebar>

      <SidebarInset>
        <header className="flex h-14 items-center justify-between gap-2 border-b border-border px-4">
          <div className="flex items-center gap-3">
            <SidebarTrigger />
            <StatusBadge tone={live ? "success" : "neutral"}>
              {live ? ar_label("Realtime connected", "متصل مباشرة", ar) : ar_label("Offline", "غير متصل", ar)}
            </StatusBadge>
          </div>
          <div className="flex items-center gap-1">
            {/* Theme picker — cycles through all presets (dark, light, purple, rose, emerald, …) */}
            <ThemePicker size="default" />

            <DropdownMenu>
              <DropdownMenuTrigger className="flex items-center gap-2 rounded-full py-1 pe-3 ps-1 outline-none transition hover:bg-accent">
                <Avatar className="h-8 w-8"><AvatarFallback>{initial}</AvatarFallback></Avatar>
                <span className="max-w-[160px] truncate text-sm">{me?.email ?? "…"}</span>
                <ChevronDown className="h-4 w-4 text-muted-foreground" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <div className="truncate px-2 py-1.5 text-xs text-muted-foreground">{me?.email ?? ""}</div>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => go("/profile")}>
                  <User className="me-2 h-4 w-4" />{ar_label("Profile", "الملف الشخصي", ar)}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem className="text-destructive" onClick={async () => { await auth.logout(); clearSession(); go("/login"); }}>
                  <LogOut className="me-2 h-4 w-4" />{ar_label("Sign out", "تسجيل الخروج", ar)}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </header>
        <main className="min-w-0 flex-1 overflow-auto"><Outlet /></main>
      </SidebarInset>
    </SidebarProvider>
    </ToastProvider>
  );
}
