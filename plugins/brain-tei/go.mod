module github.com/togo-framework/brain-tei

go 1.26

require (
	github.com/togo-framework/brain v0.0.0
	github.com/togo-framework/togo v0.21.0
)

require github.com/go-chi/chi/v5 v5.3.0 // indirect

// Dev: resolve the sibling brain module locally (monorepo). Removed when consuming
// the published github.com/togo-framework/brain.
replace github.com/togo-framework/brain => ../brain
