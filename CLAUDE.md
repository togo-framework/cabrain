# Project guidance for Claude Code

This is a **togo** app (Go + sqlc + Atlas + GraphQL/REST + Next.js). Conventions:

- Add entities with `togo make:resource <Name> field:type`, then `togo generate && togo migrate`.
- `*.gen.go` and `internal/**/gen/` are generated — never hand-edit.
- API-first: every resource is REST/OpenAPI + GraphQL. Config via `.env`/togo.yaml.
- Everything is a plugin (microkernel). Add capabilities with `togo install <owner>/<repo>`.
- Use `togo dev` for local (hot reload), `togo format` / `togo lint` for code standards.

See .claude/rules/togo.md for detail and .claude/skills for slash commands.

## The FlowOS brain (cabrain MCP)

The **cabrain** MCP server (in `.mcp.json`) exposes this project's memory organ. The
**FlowOS / OneStudio hub** knowledge is loaded into the `flowos` namespace (~1,780
memories: ventures, domain-expert agents, people, issues, posts, roadmaps, releases,
goals, learnings, harvested research).

**When the user asks anything about FlowOS / OneStudio — a venture, a person, an
agent, an issue, a decision, a learning — call the cabrain MCP `memory_recall` tool
with `namespace: "flowos"` FIRST**, then answer from what it returns (cite the
memories). Examples: "who is X?", "what is the Sentra venture?", "which agent does
Saudi HR?", "what open OAuth issues exist?", "what have we learned about auth gates?".

- Prefer concise/keyword queries (e.g. `"Sentra"`, `"PDPL compliance kit"`) — they
  rank cleaner than long sentences.
- To store a new fact, use `memory_retain` (namespace `flowos`). To fetch one by id,
  `memory_get`; to retire one, `memory_forget`.
- The brain needs the app running on `:8080` (`bash /home/coder/run-cabrain.sh` if it's
  down) and the workspace attached to `stack_stacknet`. See `docs/flowos-brain.md`.
