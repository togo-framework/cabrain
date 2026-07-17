package plugins

// togo auth — the human login gate for the web console (JWT + RBAC + multi-guard),
// plus the dev-login driver (one-click admin session, non-production only).
//
// These are PUBLISHED plugins (not in-repo), blank-imported here so their init()
// self-registers them with the kernel. Kept OUT of plugins.gen.go (which
// `togo install` owns) so codegen never clobbers this wiring. Equivalent to:
//
//	togo install togo-framework/auth
//	togo install togo-framework/auth-dev
//
// The console gate is COMPLEMENTARY to the existing MCP token ACL (X-Cabrain-Token):
// auth governs the human web console; the token ACL governs MCP agents. See the
// brain plugin's consoleAuth gate (CABRAIN_REQUIRE_AUTH).
import (
	_ "github.com/togo-framework/auth"
	_ "github.com/togo-framework/auth-dev"
)
