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
		// Health + the console read-API (always safe; defensive when the schema
		// isn't live). retain/recall return a structured "needs DB + brain-tei"
		// error until Blocker B clears.
		k.Router.Get("/api/brain/ping", svc.Ping)
		k.Router.Get("/api/brain/stats", svc.Stats)
		k.Router.Get("/api/brain/activity", svc.Activity)
		k.Router.Get("/api/brain/namespaces", svc.Namespaces)
		k.Router.Get("/api/brain/graph", svc.Graph)
		k.Router.Post("/api/brain/recall", svc.Recall)
		k.Router.Post("/api/brain/retain", svc.Retain)
		k.Set(Name, svc)
		if k.Log != nil {
			k.Log.Info("plugin active", "plugin", Name)
		}
		return nil
	})
}
