package plugins

// Local (in-repo) plugins developed in this monorepo and wired via go.work.
// Kept OUT of plugins.gen.go (which `togo install` manages) so codegen never
// clobbers it. Each in-repo plugin self-registers with the kernel on this
// blank import.
import (
	_ "github.com/togo-framework/brain"
	_ "github.com/togo-framework/brain-tei"
)
