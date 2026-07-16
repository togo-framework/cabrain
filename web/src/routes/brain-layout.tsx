import { Outlet, useNavigate, useRouterState, Link } from "@tanstack/react-router";
import { Brain, LayoutDashboard, Search, Waypoints, Database, Activity } from "lucide-react";
import {
  SidebarProvider, Sidebar, SidebarHeader, SidebarContent,
  SidebarGroup, SidebarGroupLabel, SidebarMenu, SidebarMenuItem, SidebarMenuButton,
  SidebarInset, SidebarTrigger, StatusBadge, ThemePicker,
} from "@togo-framework/ui";

const NAV = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, exact: true },
  { to: "/search", label: "Search", icon: Search },
  { to: "/graph", label: "Graph", icon: Waypoints },
  { to: "/brains", label: "Brains", icon: Database },
  { to: "/sessions", label: "Sessions", icon: Activity },
];

export function BrainLayout() {
  const nav = useNavigate();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const isActive = (to: string, exact?: boolean) => (exact ? pathname === to : pathname.startsWith(to));

  return (
    <SidebarProvider>
      <Sidebar collapsible="icon">
        <SidebarHeader>
          <Link to="/" className="flex items-center gap-2 px-2 py-1.5">
            <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground">
              <Brain className="h-4 w-4" />
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
        </SidebarContent>
      </Sidebar>

      <SidebarInset>
        <header className="flex h-14 items-center justify-between gap-2 border-b border-border px-4">
          <div className="flex items-center gap-3">
            <SidebarTrigger />
            <span className="text-sm font-medium text-muted-foreground">Memory organ</span>
          </div>
          <div className="flex items-center gap-2">
            <StatusBadge tone="neutral">togo-postgres</StatusBadge>
            <ThemePicker size="default" />
          </div>
        </header>
        <main className="min-w-0 flex-1 overflow-auto"><Outlet /></main>
      </SidebarInset>
    </SidebarProvider>
  );
}
