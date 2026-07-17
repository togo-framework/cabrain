import { Outlet, useNavigate, useRouterState, Link } from "@tanstack/react-router";
import { Database, Users, KeyRound, Search, Settings } from "lucide-react";
import {
  SidebarProvider, Sidebar, SidebarHeader, SidebarContent,
  SidebarGroup, SidebarGroupLabel, SidebarMenu, SidebarMenuItem, SidebarMenuButton,
  SidebarInset, SidebarTrigger, StatusBadge, ThemePicker,
} from "@togo-framework/ui";
import { LiveIndicator } from "../lib/realtime";
import { NeuralGlyph, NeuralBackdrop } from "../components/neural";
import { UserMenu } from "../components/chrome";

// The brain hub is the entry point. Its sidebar is deliberately small: the Brains
// hub itself, plus an Admin group (users, tokens, cross-brain search) kept out of
// the main flow. The old flat Dashboard/Search/Graph/Gaps/Sessions siblings are gone.
const HUB_NAV = [
  { to: "/", label: "Brains", icon: Database, exact: true },
];

const ADMIN_NAV = [
  { to: "/admin/users", label: "Users", icon: Users },
  { to: "/admin/tokens", label: "Tokens & ACL", icon: KeyRound },
  { to: "/admin/search", label: "Global search", icon: Search },
];

export function HubLayout() {
  const nav = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const isActive = (to: string, exact?: boolean) => (exact ? pathname === to : pathname.startsWith(to));

  return (
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
              {HUB_NAV.map((n) => (
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
        <NeuralBackdrop className="opacity-60" />
        <header className="relative z-10 flex h-14 items-center justify-between gap-2 border-b border-border px-4">
          <div className="flex items-center gap-3">
            <SidebarTrigger />
            <span className="inline-flex items-center gap-1.5 text-sm font-medium text-muted-foreground">
              <Settings className="h-3.5 w-3.5" /> Memory organ
            </span>
          </div>
          <div className="flex items-center gap-2">
            <LiveIndicator />
            <StatusBadge tone="neutral">togo-postgres</StatusBadge>
            <ThemePicker size="default" />
            <UserMenu />
          </div>
        </header>
        <main className="relative z-10 min-w-0 flex-1 overflow-auto"><Outlet /></main>
      </SidebarInset>
    </SidebarProvider>
  );
}
