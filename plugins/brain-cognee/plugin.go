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
		// Cognee's auth scheme is deployment-specific; default is a Bearer token.
		if h := os.Getenv("COGNEE_AUTH_HEADER"); h != "" {
			opts = append(opts, cognee.WithAuthHeader(h, os.Getenv("COGNEE_AUTH_PREFIX")))
		}
		c := cognee.New(base, os.Getenv("COGNEE_API_TOKEN"), opts...)
		brain.RegisterEngine(k, c)
		if k.Log != nil {
			k.Log.Info("plugin active", "plugin", Name, "cognee", base)
		}
		return nil
	})
}
