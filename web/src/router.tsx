import { createRootRoute, createRoute, createRouter, Outlet } from "@tanstack/react-router";
import { Providers } from "./providers";
import { BrainLayout } from "./routes/brain-layout";
import { BrainDashboard } from "./routes/brain-dashboard";
import { BrainSearch } from "./routes/brain-search";
import { BrainGraph } from "./routes/brain-graph";
import { BrainBrains } from "./routes/brain-brains";
import { BrainSessions } from "./routes/brain-sessions";
import { BrainGaps } from "./routes/brain-gaps";
import { BrainPermissions } from "./routes/brain-permissions";
import { BrainSecrets } from "./routes/brain-secrets";
import { BrainUsers } from "./routes/brain-users";

// CaBrain memory console — the Cognee-style surface over the brain plugin.
// Un-gated: this is a memory tool, not an auth app; the whole console lives under
// the BrainLayout shell (sidebar + header).
const rootRoute = createRootRoute({ component: () => (<Providers><Outlet /></Providers>) });

const consoleRoute = createRoute({ getParentRoute: () => rootRoute, id: "_console", component: BrainLayout });
const dashboardRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/", component: BrainDashboard });
const searchRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/search", component: BrainSearch });
const graphRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/graph", component: BrainGraph });
const brainsRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/brains", component: BrainBrains });
const gapsRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/gaps", component: BrainGaps });
const permissionsRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/permissions", component: BrainPermissions });
const secretsRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/secrets", component: BrainSecrets });
const usersRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/users", component: BrainUsers });
const sessionsRoute = createRoute({ getParentRoute: () => consoleRoute, path: "/sessions", component: BrainSessions });

const routeTree = rootRoute.addChildren([
  consoleRoute.addChildren([
    dashboardRoute, searchRoute, graphRoute, brainsRoute,
    gapsRoute, permissionsRoute, secretsRoute, usersRoute, sessionsRoute,
  ]),
]);

export const router = createRouter({ routeTree, defaultPreload: "intent" });

declare module "@tanstack/react-router" {
  interface Register { router: typeof router }
}
