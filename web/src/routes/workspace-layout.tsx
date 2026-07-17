import { Outlet, useNavigate, useParams, useRouterState, Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Network, Search, Rocket, Lock, HelpCircle, KeyRound, Activity,
  ChevronRight, ChevronsUpDown, MessagesSquare, Plug,
} from "lucide-react";
import {
  SidebarProvider, Sidebar, SidebarHeader, SidebarContent,
  SidebarGroup, SidebarGroupLabel, SidebarMenu, SidebarMenuItem, SidebarMenuButton,
  SidebarInset, SidebarTrigger, ThemePicker,
} from "@togo-framework/ui";
import { LiveIndicator } from "../lib/realtime";
import { NeuralGlyph, SynapseField, NeuralBackdrop, NeuralCellMark } from "../components/neural";
import { UserMenu } from "../components/chrome";
import { brainApi } from "../lib/brain";
import { hueForBrain } from "../lib/brain-colors";

// The brain workspace is a scoped surface: every section below is bound to the
// $namespace in the URL. Overview (the graph) is the flagship index. Sources
// (data-source connectors) feed knowledge into the brain.
const SECTIONS = [
  { seg: "", to: "/b/$namespace", label: "Overview", icon: Network },
  { seg: "chat", to: "/b/$namespace/chat", label: "Chat", icon: MessagesSquare },
  { seg: "search", to: "/b/$namespace/search", label: "Search", icon: Search },
  { seg: "sources", to: "/b/$namespace/sources", label: "Sources", icon: Plug },
  { seg: "sessions", to: "/b/$namespace/sessions", label: "Sessions", icon: Rocket },
  { seg: "secrets", to: "/b/$namespace/secrets", label: "Secrets", icon: Lock },
  { seg: "gaps", to: "/b/$namespace/gaps", label: "Gaps", icon: HelpCircle },
  { seg: "permissions", to: "/b/$namespace/permissions", label: "Permissions", icon: KeyRound },
  { seg: "activity", to: "/b/$namespace/activity", label: "Activity", icon: Activity },
] as const;

export function BrainWorkspaceLayout() {
  const nav = useNavigate();
  const { namespace } = useParams({ strict: false }) as { namespace: string };
  const pathname = useRouterState({ select: (s) => s.location.pathname });

  const base = `/b/${namespace}`;
  const rest = pathname.startsWith(base) ? pathname.slice(base.length).replace(/^\//, "") : "";
  const current = SECTIONS.find((s) => s.seg === rest) ?? SECTIONS[0];
  // The brain's identity colour, threaded through the whole workspace chrome.
  const accent = hueForBrain(namespace || "brain");

  const namespaces = useQuery({ queryKey: ["brain", "namespaces"], queryFn: brainApi.namespaces });
  const brains = namespaces.data?.brains ?? [];

  const isActive = (seg: string) => (seg === "" ? pathname === base : rest === seg);

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
            {/* Brain identity — avatar + colour + name (Shape-of-AI: Identifiers) */}
            <SidebarGroupLabel className="gap-2 truncate">
              <span className="inline-flex items-center gap-1.5">
                <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: accent, boxShadow: `0 0 8px ${accent}` }} />
                <span className="truncate">Brain · {namespace}</span>
              </span>
            </SidebarGroupLabel>
            <SidebarMenu>
              {SECTIONS.map((s) => (
                <SidebarMenuItem key={s.label}>
                  <SidebarMenuButton
                    isActive={isActive(s.seg)}
                    tooltip={s.label}
                    onClick={() => nav({ to: s.to, params: { namespace } })}
                  >
                    <s.icon className="h-4 w-4" />
                    <span>{s.label}</span>
                  </SidebarMenuButton>
                </SidebarMenuItem>
              ))}
            </SidebarMenu>
          </SidebarGroup>
        </SidebarContent>
      </Sidebar>

      <SidebarInset>
        {/* Ambient neural mesh behind the whole workspace — one living organism. */}
        <NeuralBackdrop className="opacity-60" />
        <header className="relative z-10 flex h-14 items-center justify-between gap-2 overflow-hidden border-b border-border px-4">
          <SynapseField className="opacity-[0.15]" />
          {/* thin accent line in the brain's own hue */}
          <span className="pointer-events-none absolute inset-x-0 bottom-0 h-px" style={{ background: `linear-gradient(90deg, transparent, ${accent}, transparent)` }} />
          <div className="relative flex min-w-0 items-center gap-2">
            <SidebarTrigger />
            {/* Breadcrumb */}
            <nav className="flex min-w-0 items-center gap-1.5 text-sm">
              <Link to="/" className="text-muted-foreground hover:text-foreground">Brains</Link>
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              {/* Brain switcher — carries the brain's neural avatar + colour */}
              <div className="relative inline-flex items-center">
                <span className="pointer-events-none absolute left-1.5 flex items-center"><NeuralCellMark color={accent} size={20} firing={false} /></span>
                <select
                  value={namespace}
                  onChange={(e) => nav({ to: current.to, params: { namespace: e.target.value } })}
                  className="max-w-[180px] appearance-none truncate rounded-lg border bg-background py-1.5 pl-8 pr-7 text-sm font-medium text-foreground outline-none"
                  style={{ borderColor: `${accent}55` }}
                  title="Switch brain"
                >
                  {brains.length === 0 && <option value={namespace}>{namespace}</option>}
                  {brains.map((b) => (
                    <option key={b.namespace} value={b.namespace}>{b.namespace}</option>
                  ))}
                </select>
                <ChevronsUpDown className="pointer-events-none absolute right-2 h-3.5 w-3.5 text-muted-foreground" />
              </div>
              <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
              <span className="truncate font-medium text-foreground">{current.label}</span>
            </nav>
          </div>
          <div className="relative flex items-center gap-2">
            <LiveIndicator />
            <ThemePicker size="default" />
            <UserMenu />
          </div>
        </header>
        <main className="relative z-10 min-w-0 flex-1 overflow-auto"><Outlet /></main>
      </SidebarInset>
    </SidebarProvider>
  );
}
