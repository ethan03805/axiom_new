# Runtime Skill System Reference

Phase 17 adds deterministic runtime instruction generation for supported orchestrators. The goal is not just to document Axiom, but to make Claude Code, Codex, OpenCode, and Claw behave as Axiom orchestrators instead of silently doing the work outside the engine.

Current operating model: these generated artifacts are how you appoint the orchestrator yourself. Axiom does not auto-launch an embedded orchestrator in normal app flows today.

The implementation lives in:

- `internal/skill/generator.go` - runtime artifact generation
- `internal/cli/skill.go` - `axiom skill generate --runtime <...>` CLI wiring

## Command

```bash
axiom skill generate --runtime <claw|claude-code|codex|opencode>
```

The command writes runtime-specific artifacts into the repository and returns the list of generated files.

## Supported Runtimes

| Runtime | Primary mechanism | Generated artifacts |
|---------|-------------------|---------------------|
| `claw` | Markdown skill injection | `axiom-skill.md` |
| `claude-code` | `CLAUDE.md` + hooks | `AGENTS.md`, `.agents/skills/axiom-runtime/SKILL.md`, `.claude/skills/axiom-runtime/SKILL.md`, `.claude/CLAUDE.md`, `.claude/settings.json`, `.claude/hooks/axiom-guard.py` |
| `codex` | `AGENTS.md` + repo skill | `AGENTS.md`, `.agents/skills/axiom-runtime/SKILL.md`, `codex-instructions.md` |
| `opencode` | `AGENTS.md` + `opencode.json` | `AGENTS.md`, `.agents/skills/axiom-runtime/SKILL.md`, `opencode-instructions.md`, `opencode.json` |

## Shared Contract Content

Every generated runtime artifact includes the same Axiom contract:

- The runtime is acting as an Axiom orchestrator, not as the implementation worker.
- The runtime must not implement the user's software request directly outside Axiom.
- The required workflow is `prompt -> SRS -> approval -> execution`.
- The Trusted Engine vs. Untrusted Agent Plane boundary.
- REST, WebSocket, and IPC request surfaces used by orchestrators.
- TaskSpec construction rules and context-tier guidance.
- ReviewSpec rules and reviewer mandate.
- Budget and model-registry rules.
- Task decomposition principles.
- Communication model restrictions.
- ECO categories and approval flow.
- Test authorship separation requirements.
- Error handling and escalation rules.

The generated text also includes config-derived values from the effective loaded configuration (project overrides layered on top of global config and built-in defaults), including:

- API base URL from `[api].port`
- Budget ceiling and warning threshold from `[budget]`
- Work branch naming from `[git].branch_prefix`

## Runtime-Specific Enforcement

### Claw

`axiom-skill.md` is a single markdown artifact for direct Claw skill injection. It carries the full orchestrator contract without additional runtime glue.

### Claude Code

Claude Code is the strictest case because its instruction files are advisory unless paired with hooks.

Generated files:

- `.claude/CLAUDE.md` - Claude-specific Axiom contract
- `.claude/settings.json` - registers a `PreToolUse` hook
- `.claude/hooks/axiom-guard.py` - blocks `Edit`, `Write`, `MultiEdit`, and non-`axiom ...` shell execution
- `AGENTS.md` and repo skill files - shared Axiom contract for cross-runtime consistency

The hook intentionally allows `axiom ...` shell commands while rejecting direct implementation work outside Axiom.

### Codex

Codex currently discovers repository instructions through `AGENTS.md` and repo skills under `.agents/skills/`. For that reason the generated Codex setup is:

- `AGENTS.md` as the primary instruction source
- `.agents/skills/axiom-runtime/SKILL.md` for explicit or implicit skill activation
- `codex-instructions.md` as an architecture-compatible companion artifact

The practical effect is that Codex sees the Axiom contract immediately from the repository root and also has a repo-local skill for workflow reinforcement.

### OpenCode

OpenCode also works best with repository-root `AGENTS.md`, but unlike Codex it supports explicit config-level instruction loading and tool permissions.

Generated files:

- `AGENTS.md`
- `.agents/skills/axiom-runtime/SKILL.md`
- `opencode-instructions.md`
- `opencode.json`

`opencode.json` currently:

- loads `opencode-instructions.md` through the `instructions` field
- sets `permission.edit = "ask"`
- sets `permission.bash = "ask"`

That approval gating is part of the deterministic compliance strategy: OpenCode should not be able to silently bypass Axiom by editing files or running arbitrary shell commands.

## Regeneration Rules

Re-run `axiom skill generate` after changing effective configuration inputs, especially:

- `[orchestrator].runtime`
- `[api].port`
- `[budget].max_usd`
- `[budget].warn_at_percent`
- `[git].branch_prefix`
- any global defaults in `~/.axiom/config.toml` that you expect generated artifacts to reflect

The generator is config-aware, but it is not currently file-watch-driven. Regeneration is an explicit command, not an automatic background process.

## Managed Files

Generated markdown files begin with:

```text
<!-- axiom:managed by `axiom skill generate` -->
```

Treat generated files as managed outputs. If you need to refresh them, rerun the command instead of hand-editing and expecting the changes to survive regeneration.

## Recommended Workflow

1. Initialize the project with `axiom init`.
2. Set the desired orchestrator runtime in `.axiom/config.toml` or `~/.axiom/config.toml`, depending on whether you want a project-local override or a user-wide default.
3. Generate the matching runtime artifacts with `axiom skill generate --runtime <runtime>`.
4. If using external orchestration, generate an API token and start the API server.
5. Point the runtime at the repository root so it loads the generated instruction files.
6. Re-run the generator when relevant config values change.

## Related Docs

- [CLI Reference](cli-reference.md)
- [Configuration Reference](configuration.md)
- [API Server](api-server.md)
- [Getting Started](getting-started.md)
- [Security, Secret Handling, and Prompt Safety](security-prompt-safety.md)
