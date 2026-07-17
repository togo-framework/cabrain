// Package braincognee is the Cognee provider plugin for CaBrain: it publishes an
// Engine (the cognify graph pipeline) onto the kernel for the brain plugin to call
// off the hot path when a memory is retained. Config from env (COGNEE_API_URL /
// COGNEE_API_TOKEN, optional COGNEE_AUTH_HEADER / COGNEE_AUTH_PREFIX).
// Self-registers on blank-import.
package braincognee

import (
	"os"

	"github.com/togo-framework/togo"

	"github.com/togo-framework/brain"
	"github.com/togo-framework/brain-cognee/internal/cognee"
)

const Name = "brain-cognee"

func init() {
	togo.RegisterProviderFunc(Name, togo.PriorityLate, func(k *togo.Kernel) error {
		base := os.Getenv("COGNEE_API_URL")
		if base == "" {
			if k.Log != nil {
				k.Log.Warn("brain-cognee: COGNEE_API_URL unset — cognify engine disabled")
			}
			return nil
		}
		opts := []cognee.Option{}
		if k.Log != nil {
			opts = append(opts, cognee.WithWarnFunc(k.Log.Warn))
		}
		// Cognee (fastapi-users) authenticates via a login form (email + password)
		// that returns a JWT; the client logs in lazily and caches it.
		email := os.Getenv("COGNEE_ADMIN_EMAIL")
		password := os.Getenv("COGNEE_API_TOKEN")
		c := cognee.New(base, email, password, opts...)
		brain.RegisterEngine(k, c)
		if k.Log != nil {
			k.Log.Info("plugin active", "plugin", Name, "cognee", base, "user", email)
		}
		return nil
	})
}
