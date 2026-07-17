import { Outlet, useNavigate, useRouterState, Link } from "@tanstack/react-router";
import { Users, KeyRound, Search, ChevronRight } from "lucide-react";
import {
  SidebarProvider, Sidebar, SidebarHeader, SidebarContent,
  SidebarGroup, SidebarGroupLabel, SidebarMenu, SidebarMenuItem, SidebarMenuButton,
  SidebarInset, SidebarTrigger, ThemePicker,
} from "@togo-framework/ui";
import { LiveIndicator } from "../lib/realtime";
import { NeuralGlyph } from "../components/neural";
import { UserMenu } from "../components/chrome";

// Cross-brain admin, kept out of the main brain flow. Users + Tokens/ACL are the
// global controls; a global cross-brain search lives here too.
const ADMIN_NAV = [
  { to: "/admin/users", label: "Users", icon: Users },
  { to: "/admin/tokens", label: "Tokens & ACL", icon: KeyRound },
  { to: "/admin/search", label: "Global search", icon: Search },
];

const LABELS: Record<string, string> = {
  "/admin/users": "Users",
  "/admin/tokens": "Tokens & ACL",
  "/admin/search": "Global search",
};

export function AdminLayout() {
  const nav = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const isActive = (to: string) => pathname.startsWith(to);
  const crumb = LABELS[pathname] ?? "Admin";

  return (
    <SidebarProvider>
      <Sidebar collapsible="icon">
        <SidebarHeader>
          <Link to="/" className="flex items-center gap-2 px-2 py-1.5" title="Back to Brains">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-indigo-500 via-violet-500 to-teal-400 text-white shadow-[0_0_18px_-4px] shadow-violet-500/50">
              <NeuralGlyph className="h-5 w-5" />
            </span>
            <span className="truncate font-semibold group-data-[collapsible=icon]:hidden">CaBrain</span>
          </Link>
        </SidebarHeader>
        <SidebarContent>
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
          <div className="flex items-center gap-2">
            <SidebarTrigger />
            <nav className="flex items-center gap-1.5 text-sm">
              <Link to="/" className="text-muted-foreground hover:text-foreground">Brains</Link>
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span className="font-medium text-foreground">{crumb}</span>
            </nav>
          </div>
          <div className="flex items-center gap-2">
            <LiveIndicator />
            <ThemePicker size="default" />
            <UserMenu />
          </div>
        </header>
        <main className="min-w-0 flex-1 overflow-auto"><Outlet /></main>
      </SidebarInset>
    </SidebarProvider>
  );
}
