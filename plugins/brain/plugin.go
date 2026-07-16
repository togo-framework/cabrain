// Package brain is the CaBrain memory-organ togo plugin (a mini-app). It
// self-registers on blank-import via `togo install`. Backend logic lives in
// internal/brain; UI in web/. Provider integrations (TEI embeddings/rerank,
// the Cognee cognify engine, the object-store cold tier) plug in behind
// interfaces as their own driver plugins (brain-tei, brain-cognee, …).
package brain

import (
	"github.com/togo-framework/togo"

	"github.com/togo-framework/brain/internal/brain"
)

const Name = "brain"

func init() {
	togo.RegisterProviderFunc(Name, togo.PriorityLate, func(k *togo.Kernel) error {
		svc := brain.New(k)
		// Health probe; the memory surface (retain/recall/…) is wired as it lands.
		k.Router.Get("/api/brain/ping", svc.Ping)
		k.Set(Name, svc)
		if k.Log != nil {
			k.Log.Info("plugin active", "plugin", Name)
		}
		return nil
	})
}
