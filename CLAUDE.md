# Project guidance for Claude Code

This is a **togo** app (Go + sqlc + Atlas + GraphQL/REST + Next.js). Conventions:

- Add entities with `togo make:resource <Name> field:type`, then `togo generate && togo migrate`.
- `*.gen.go` and `internal/**/gen/` are generated — never hand-edit.
- API-first: every resource is REST/OpenAPI + GraphQL. Config via `.env`/togo.yaml.
- Everything is a plugin (microkernel). Add capabilities with `togo install <owner>/<repo>`.
- Use `togo dev` for local (hot reload), `togo format` / `togo lint` for code standards.

See .claude/rules/togo.md for detail and .claude/skills for slash commands.
