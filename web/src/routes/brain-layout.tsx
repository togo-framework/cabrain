import { Outlet, useNavigate, useRouterState, Link } from "@tanstack/react-router";
import { LayoutDashboard, Search, Waypoints, Database, Activity, HelpCircle, KeyRound, Lock, Users } from "lucide-react";
import {
  SidebarProvider, Sidebar, SidebarHeader, SidebarContent,
  SidebarGroup, SidebarGroupLabel, SidebarMenu, SidebarMenuItem, SidebarMenuButton,
  SidebarInset, SidebarTrigger, StatusBadge, ThemePicker,
} from "@togo-framework/ui";
import { RealtimeProvider, LiveIndicator } from "../lib/realtime";
import { NeuralGlyph } from "../components/neural";

// Memory surface + Admin surface. Admin (permissions/users) is grouped apart so
// the trust boundary reads at a glance.
const NAV = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, exact: true },
  { to: "/search", label: "Search", icon: Search },
  { to: "/graph", label: "Graph", icon: Waypoints },
  { to: "/brains", label: "Brains", icon: Database },
  { to: "/gaps", label: "Gaps", icon: HelpCircle },
  { to: "/sessions", label: "Sessions", icon: Activity },
];

const ADMIN_NAV = [
  { to: "/permissions", label: "Permissions", icon: KeyRound },
  { to: "/secrets", label: "Secrets", icon: Lock },
  { to: "/users", label: "Users", icon: Users },
];

export function BrainLayout() {
  const nav = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const isActive = (to: string, exact?: boolean) => (exact ? pathname === to : pathname.startsWith(to));

  return (
    <RealtimeProvider>
      <SidebarProvider>
        <Sidebar collapsible="icon">
          <SidebarHeader>
            <Link to="/" className="flex items-center gap-2 px-2 py-1.5">
              <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-indigo-500 via-violet-500 to-teal-400 text-white shadow-[0_0_18px_-4px] shadow-violet-500/50">
                <NeuralGlyph className="h-5 w-5" />
              </span>
              <span className="truncate font-semibold group-data-[collapsible=icon]:hidden">CaBrain</span>
            </Link>
          </SidebarHeader>
          <SidebarContent>
            <SidebarGroup>
              <SidebarGroupLabel>Memory</SidebarGroupLabel>
              <SidebarMenu>
                {NAV.map((n) => (
                  <SidebarMenuItem key={n.to}>
                    <SidebarMenuButton isActive={isActive(n.to, n.exact)} tooltip={n.label} onClick={() => nav({ to: n.to })}>
                      <n.icon className="h-4 w-4" />
                      <span>{n.label}</span>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroup>
            <SidebarGroup>
              <SidebarGroupLabel>Admin</SidebarGroupLabel>
              <SidebarMenu>
                {ADMIN_NAV.map((n) => (
                  <SidebarMenuItem key={n.to}>
                    <SidebarMenuButton isActive={isActive(n.to)} tooltip={n.label} onClick={() => nav({ to: n.to })}>
                      <n.icon className="h-4 w-4" />
                      <span>{n.label}</span>
                    </SidebarMenuButton>
                  </SidebarMenuItem>
                ))}
              </SidebarMenu>
            </SidebarGroup>
          </SidebarContent>
        </Sidebar>

        <SidebarInset>
          <header className="flex h-14 items-center justify-between gap-2 border-b border-border px-4">
            <div className="flex items-center gap-3">
              <SidebarTrigger />
              <span className="text-sm font-medium text-muted-foreground">Memory organ</span>
            </div>
            <div className="flex items-center gap-2">
              <LiveIndicator />
              <StatusBadge tone="neutral">togo-postgres</StatusBadge>
              <ThemePicker size="default" />
            </div>
          </header>
          <main className="min-w-0 flex-1 overflow-auto"><Outlet /></main>
        </SidebarInset>
      </SidebarProvider>
    </RealtimeProvider>
  );
}
