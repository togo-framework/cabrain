import { createRootRoute, createRoute, createRouter, redirect, useParams, Outlet } from "@tanstack/react-router";
import { Providers } from "./providers";
import { AuthGate } from "./routes/auth-gate";
import { RealtimeProvider } from "./lib/realtime";
import { HubLayout } from "./routes/hub-layout";
import { BrainWorkspaceLayout } from "./routes/workspace-layout";
import { AdminLayout } from "./routes/admin-layout";
import { BrainsHub } from "./routes/brain-brains";
import { BrainOverview } from "./routes/brain-overview";
import { BrainChat } from "./routes/brain-chat";
import { BrainSearch } from "./routes/brain-search";
import { BrainSources } from "./routes/brain-sources";
import { BrainSessions } from "./routes/brain-sessions";
import { BrainSecrets } from "./routes/brain-secrets";
import { BrainGaps } from "./routes/brain-gaps";
import { BrainActivity } from "./routes/brain-activity";
import { BrainWorkspacePermissions } from "./routes/brain-workspace-permissions";
import { BrainUsers } from "./routes/brain-users";
import { BrainPermissions } from "./routes/brain-permissions";
import { ProfilePage } from "./routes/profile-page";

// CaBrain memory console — everything is wired over the brain. The BRAIN is the
// main entry: "/" is the Brains hub, each brain opens a scoped workspace at
// /b/$namespace (graph/mindmap centerpiece + its own sections), and cross-brain
// admin (users, tokens, global search) lives out of the main flow under /admin.
//
// The whole console is wrapped by <AuthGate>: when the backend enforces auth
// (CABRAIN_REQUIRE_AUTH) and there is no session, the login page replaces it;
// otherwise it renders as-is. RealtimeProvider (single shared SSE stream) wraps
// the authed content so every layout live-updates.
const rootRoute = createRootRoute({
  component: () => (
    <Providers>
      <AuthGate>
        <RealtimeProvider>
          <Outlet />
        </RealtimeProvider>
      </AuthGate>
    </Providers>
  ),
});

// Read the $namespace route param for the scoped workspace pages.
function useNamespace(): string {
  return (useParams({ strict: false }) as { namespace?: string }).namespace ?? "";
}

// ── Hub (the entry point) ────────────────────────────────────────────────────
const hubLayoutRoute = createRoute({ getParentRoute: () => rootRoute, id: "_hub", component: HubLayout });
const hubIndexRoute = createRoute({ getParentRoute: () => hubLayoutRoute, path: "/", component: BrainsHub });
// User profile & settings — rendered inside the hub chrome (sidebar + header).
const profileRoute = createRoute({ getParentRoute: () => hubLayoutRoute, path: "/profile", component: ProfilePage });

// Old flat nav → sensible redirects so deep links don't break.
const mkRedirect = (path: string, to: string) =>
  createRoute({
    getParentRoute: () => rootRoute,
    path,
    beforeLoad: () => { throw redirect({ to }); },
    component: () => null,
  });
const redirects = [
  mkRedirect("/dashboard", "/"),
  mkRedirect("/brains", "/"),
  mkRedirect("/search", "/admin/search"),
  mkRedirect("/graph", "/"),
  mkRedirect("/gaps", "/"),
  mkRedirect("/sessions", "/"),
  mkRedirect("/secrets", "/"),
  mkRedirect("/permissions", "/admin/tokens"),
  mkRedirect("/users", "/admin/users"),
];

// ── Brain workspace (scoped to $namespace) ───────────────────────────────────
const workspaceLayoutRoute = createRoute({ getParentRoute: () => rootRoute, path: "/b/$namespace", component: BrainWorkspaceLayout });
const wsOverviewRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/", component: () => <BrainOverview namespace={useNamespace()} /> });
const wsChatRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/chat", component: BrainChat });
const wsSearchRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/search", component: () => <BrainSearch namespace={useNamespace()} /> });
const wsSourcesRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/sources", component: () => <BrainSources namespace={useNamespace()} /> });
const wsSessionsRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/sessions", component: () => <BrainSessions namespace={useNamespace()} /> });
const wsSecretsRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/secrets", component: () => <BrainSecrets namespace={useNamespace()} /> });
const wsGapsRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/gaps", component: () => <BrainGaps namespace={useNamespace()} /> });
const wsPermissionsRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/permissions", component: () => <BrainWorkspacePermissions namespace={useNamespace()} /> });
const wsActivityRoute = createRoute({ getParentRoute: () => workspaceLayoutRoute, path: "/activity", component: () => <BrainActivity namespace={useNamespace()} /> });

// ── Global admin (out of the main flow) ──────────────────────────────────────
const adminLayoutRoute = createRoute({ getParentRoute: () => rootRoute, path: "/admin", component: AdminLayout });
const adminIndexRoute = createRoute({
  getParentRoute: () => adminLayoutRoute, path: "/",
  beforeLoad: () => { throw redirect({ to: "/admin/users" }); },
  component: () => null,
});
const adminUsersRoute = createRoute({ getParentRoute: () => adminLayoutRoute, path: "/users", component: BrainUsers });
const adminTokensRoute = createRoute({ getParentRoute: () => adminLayoutRoute, path: "/tokens", component: BrainPermissions });
const adminSearchRoute = createRoute({ getParentRoute: () => adminLayoutRoute, path: "/search", component: () => <BrainSearch /> });

const routeTree = rootRoute.addChildren([
  hubLayoutRoute.addChildren([hubIndexRoute, profileRoute]),
  ...redirects,
  workspaceLayoutRoute.addChildren([
    wsOverviewRoute, wsChatRoute, wsSearchRoute, wsSourcesRoute, wsSessionsRoute, wsSecretsRoute,
    wsGapsRoute, wsPermissionsRoute, wsActivityRoute,
  ]),
  adminLayoutRoute.addChildren([
    adminIndexRoute, adminUsersRoute, adminTokensRoute, adminSearchRoute,
  ]),
]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });

declare module "@tanstack/react-router" {
  interface Register { router: typeof router }
}
