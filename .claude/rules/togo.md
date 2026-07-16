# togo conventions

- Entities are added with `togo make:resource`; `togo.resources.yaml` is the source of truth.
- `*.gen.go` and `internal/**/gen/` are generated — never hand-edit.
- `togo generate` runs sqlc -> gqlgen -> atlas -> OpenAPI (the build gate).
- API-first: every resource is exposed over REST/OpenAPI and GraphQL.
- Config is dynamic (togo.yaml + .env); never hard-code URLs/connections.
- Everything is a plugin; the kernel is a microkernel. Add features with `togo install`.
