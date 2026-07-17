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

**MEMORY-FIRST IS MANDATORY. Every turn: recall → answer/act → retain.** For ANY
question or task touching FlowOS / OneStudio — a venture, portfolio, person, agent,
issue, task, post, decision, or learning — you **MUST** call the cabrain MCP
`memory_recall` tool with `namespace: "flowos"` **before** answering, base the answer
on what it returns (and cite it), and `memory_retain` anything new you produce. If
recall returns nothing, say so — never invent facts.

Examples that must trigger a recall first: "who is X?", "what is the Sentra venture?",
"which agent does Saudi HR?", "what open OAuth issues exist?", "what have we learned
about auth gates?".

- Prefer concise/keyword queries (e.g. `"Sentra"`, `"PDPL compliance kit"`).
- `memory_retain` to store, `memory_get` to fetch by id, `memory_forget` to retire.
- Needs the app on `:8080` (`bash /home/coder/run-cabrain.sh` if down) + the workspace
  on `stack_stacknet`.

**Full mandatory ruleset: `.claude/rules/memory-first.md` (R1–R8).** Also see
`docs/flowos-brain.md`.
