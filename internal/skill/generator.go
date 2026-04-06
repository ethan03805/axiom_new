package skill

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openaxiom/axiom/internal/config"
)

const managedHeader = "<!-- axiom:managed by `axiom skill generate` -->"

// Runtime identifies a supported orchestrator runtime.
type Runtime string

const (
	RuntimeClaw       Runtime = "claw"
	RuntimeClaudeCode Runtime = "claude-code"
	RuntimeCodex      Runtime = "codex"
	RuntimeOpenCode   Runtime = "opencode"
)

// Artifact describes a generated file.
type Artifact struct {
	Path string
}

// Generator writes runtime-specific Axiom instruction artifacts.
type Generator struct {
	root string
	cfg  *config.Config
}

// NewGenerator creates a new runtime artifact generator.
func NewGenerator(root string, cfg *config.Config) *Generator {
	return &Generator{root: root, cfg: cfg}
}

// Generate writes the artifacts for a given runtime and returns the files written.
func (g *Generator) Generate(runtime Runtime) ([]Artifact, error) {
	if g == nil || g.cfg == nil {
		return nil, fmt.Errorf("generator config is required")
	}
	if g.root == "" {
		return nil, fmt.Errorf("generator root is required")
	}
	if !runtime.Valid() {
		return nil, fmt.Errorf("invalid runtime %q (valid: claw, claude-code, codex, opencode)", runtime)
	}

	files, err := g.render(runtime)
	if err != nil {
		return nil, err
	}

	artifacts := make([]Artifact, 0, len(files))
	for rel, content := range files {
		path := filepath.Join(g.root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("creating directory for %s: %w", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("writing %s: %w", rel, err)
		}
		artifacts = append(artifacts, Artifact{Path: rel})
	}

	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].Path < artifacts[j].Path
	})
	return artifacts, nil
}

// Valid reports whether the runtime is supported.
func (r Runtime) Valid() bool {
	switch r {
	case RuntimeClaw, RuntimeClaudeCode, RuntimeCodex, RuntimeOpenCode:
		return true
	default:
		return false
	}
}

func (g *Generator) render(runtime Runtime) (map[string]string, error) {
	files := map[string]string{}

	switch runtime {
	case RuntimeClaw:
		files["axiom-skill.md"] = g.renderClawSkill()
	case RuntimeClaudeCode:
		files["AGENTS.md"] = g.renderAgents()
		files[filepath.Join(".agents", "skills", "axiom-runtime", "SKILL.md")] = g.renderSharedSkill()
		files[filepath.Join(".claude", "skills", "axiom-runtime", "SKILL.md")] = g.renderSharedSkill()
		files[filepath.Join(".claude", "CLAUDE.md")] = g.renderClaude()
		settings, err := g.renderClaudeSettings()
		if err != nil {
			return nil, err
		}
		files[filepath.Join(".claude", "settings.json")] = settings
		files[filepath.Join(".claude", "hooks", "axiom-guard.py")] = renderClaudeHookScript()
	case RuntimeCodex:
		files["AGENTS.md"] = g.renderAgents()
		files[filepath.Join(".agents", "skills", "axiom-runtime", "SKILL.md")] = g.renderSharedSkill()
		files["codex-instructions.md"] = g.renderCodex()
	case RuntimeOpenCode:
		files["AGENTS.md"] = g.renderAgents()
		files[filepath.Join(".agents", "skills", "axiom-runtime", "SKILL.md")] = g.renderSharedSkill()
		files["opencode-instructions.md"] = g.renderOpenCode()
		opencodeCfg, err := g.renderOpenCodeConfig()
		if err != nil {
			return nil, err
		}
		files["opencode.json"] = opencodeCfg
	default:
		return nil, fmt.Errorf("invalid runtime %q (valid: claw, claude-code, codex, opencode)", runtime)
	}

	return files, nil
}

func (g *Generator) renderClawSkill() string {
	var b strings.Builder
	b.WriteString(managedHeader + "\n")
	b.WriteString("# Axiom Runtime Skill for Claw\n\n")
	b.WriteString("This file teaches a Claw runtime to behave as an Axiom orchestrator instead of acting as a direct implementer.\n\n")
	b.WriteString(g.sharedContract())
	return b.String()
}

func (g *Generator) renderAgents() string {
	var b strings.Builder
	b.WriteString(managedHeader + "\n")
	b.WriteString("# Axiom Runtime Contract\n\n")
	b.WriteString("This `AGENTS.md` file is the shared source of truth for Codex and OpenCode, and can be imported by Claude Code.\n\n")
	b.WriteString(g.sharedContract())
	b.WriteString("## Runtime-Specific Reminder\n\n")
	b.WriteString("- For Codex and OpenCode, keep this file concise and imperative so it stays inside the instruction budget.\n")
	b.WriteString("- For Claude Code, mirror the same rules in `.claude/CLAUDE.md` and enforce them with hooks.\n")
	return b.String()
}

func (g *Generator) renderClaude() string {
	var b strings.Builder
	b.WriteString(managedHeader + "\n")
	b.WriteString("# Claude Code Axiom Contract\n\n")
	b.WriteString("Claude Code must operate as an Axiom orchestrator, not as the implementation worker.\n\n")
	b.WriteString("## Claude Code Guardrails\n\n")
	b.WriteString("- Use hooks to block direct `Edit`, `Write`, `MultiEdit`, and non-Axiom `Bash` usage.\n")
	b.WriteString("- Keep project edits inside Axiom's worker/reviewer pipeline.\n")
	b.WriteString("- If the user asks for implementation, translate that into Axiom lifecycle actions instead of changing source files directly.\n\n")
	b.WriteString(g.sharedContract())
	return b.String()
}

func (g *Generator) renderCodex() string {
	var b strings.Builder
	b.WriteString(managedHeader + "\n")
	b.WriteString("# Codex Axiom Companion Instructions\n\n")
	b.WriteString("Codex should treat `AGENTS.md` as the primary instruction source. This companion file exists for Architecture Section 25 compatibility and to reinforce the same contract in any fallback workflow.\n\n")
	b.WriteString("## Codex-Specific Usage\n\n")
	b.WriteString("- Keep `AGENTS.md` in the repository root so Codex loads it automatically.\n")
	b.WriteString("- Keep the repo skill at `.agents/skills/axiom-runtime/SKILL.md` so Codex can explicitly or implicitly load the Axiom workflow when the user says to use Axiom.\n")
	b.WriteString("- If the user asks for work outside Axiom, confirm that intent. Otherwise stay inside the Axiom contract below.\n\n")
	b.WriteString(g.sharedContract())
	return b.String()
}

func (g *Generator) renderOpenCode() string {
	var b strings.Builder
	b.WriteString(managedHeader + "\n")
	b.WriteString("# OpenCode Axiom Companion Instructions\n\n")
	b.WriteString("OpenCode should load `AGENTS.md` from the repository root and also load this file through `opencode.json` so the Axiom contract is not optional.\n\n")
	b.WriteString("## OpenCode-Specific Usage\n\n")
	b.WriteString("- Keep `permission.edit = \"ask\"` and `permission.bash = \"ask\"` so OpenCode cannot silently bypass Axiom.\n")
	b.WriteString("- Keep the repo skill at `.agents/skills/axiom-runtime/SKILL.md` available so the agent can explicitly load the Axiom workflow.\n")
	b.WriteString("- Prefer OpenCode's plan-oriented behavior for reasoning, then hand implementation to Axiom workers.\n\n")
	b.WriteString(g.sharedContract())
	return b.String()
}

func (g *Generator) renderSharedSkill() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: axiom-runtime\n")
	b.WriteString("description: Use this whenever the user says to use Axiom, asks for deterministic Axiom orchestration, or needs the runtime to delegate implementation to Axiom instead of doing the work directly.\n")
	b.WriteString("---\n\n")
	b.WriteString("Follow the Axiom runtime contract below. Treat it as mandatory.\n\n")
	b.WriteString(g.sharedContract())
	return b.String()
}

func (g *Generator) renderClaudeSettings() (string, error) {
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{
				{
					"matcher": "Edit|Write|MultiEdit|Bash",
					"hooks": []map[string]string{
						{
							"type":    "command",
							"command": "python .claude/hooks/axiom-guard.py",
						},
					},
				},
			},
		},
	}
	return marshalJSON(settings)
}

func renderClaudeHookScript() string {
	return strings.Join([]string{
		"#!/usr/bin/env python3",
		`import json`,
		`import sys`,
		"",
		"payload = json.load(sys.stdin)",
		`tool_name = payload.get("tool_name", "")`,
		`tool_input = payload.get("tool_input", {})`,
		`command = tool_input.get("command", "")`,
		`allow_axiom_bash = tool_name == "Bash" and command.strip().startswith("axiom ")`,
		`if allow_axiom_bash:`,
		`    print(json.dumps({"decision": "approve"}))`,
		`    raise SystemExit(0)`,
		`reason = "Axiom runtime policy: do not edit files or run non-Axiom shell commands directly. Route implementation through Axiom."`,
		`print(json.dumps({"decision": "block", "reason": reason}))`,
	}, "\n") + "\n"
}

func (g *Generator) renderOpenCodeConfig() (string, error) {
	cfg := map[string]any{
		"$schema":      "https://opencode.ai/config.json",
		"instructions": []string{"opencode-instructions.md"},
		"permission": map[string]any{
			"edit": "ask",
			"bash": "ask",
			"skill": map[string]string{
				"axiom-runtime": "allow",
			},
		},
	}
	return marshalJSON(cfg)
}

func marshalJSON(v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func (g *Generator) sharedContract() string {
	var b strings.Builder
	b.WriteString("## Non-Negotiable Runtime Contract\n\n")
	b.WriteString("- You are acting as an Axiom orchestrator runtime.\n")
	b.WriteString("- Do not implement the user's requested software directly outside Axiom.\n")
	b.WriteString("- Do not edit project source files directly to satisfy the user's task.\n")
	b.WriteString("- Do not run git, Docker, database, or build/test mutations directly unless the action is an Axiom command or an engine-approved read-only inspection.\n")
	b.WriteString("- If the user explicitly says to use Axiom, treat bypassing Axiom as a policy violation.\n")
	b.WriteString("- If Axiom is unavailable or blocked, stop and explain the blocker instead of silently doing the work yourself.\n\n")

	b.WriteString("## Workflow\n\n")
	b.WriteString("prompt -> SRS -> approval -> execution\n\n")
	b.WriteString("1. Capture the user intent and start or resume the Axiom run.\n")
	b.WriteString("2. Wait for SRS generation and require approval before execution.\n")
	b.WriteString("3. Decompose work into engine-tracked tasks; do not execute code changes directly in the orchestrator runtime.\n")
	b.WriteString("4. Let Axiom workers implement, reviewers review, validators validate, and the merge queue serialize integration.\n")
	b.WriteString("5. Report engine-authored status, review, ECO, and budget outcomes back to the user.\n\n")

	b.WriteString("## Trusted Engine vs. Untrusted Agent Plane\n\n")
	b.WriteString("- Trusted Engine: filesystem writes, git, Docker, SQLite, model brokering, budget enforcement, merge queue, event log.\n")
	b.WriteString("- Untrusted Agent Plane: orchestrator, sub-orchestrators, Meeseeks, reviewers. These propose actions only.\n")
	b.WriteString("- Contract: LLM agents propose, the engine disposes. Every privileged action must go through Axiom.\n\n")

	b.WriteString("## Preferred Runtime Actions\n\n")
	b.WriteString(fmt.Sprintf("- Primary API base URL: http://localhost:%d\n", g.cfg.API.Port))
	b.WriteString("- Preferred lifecycle commands: `axiom run`, `axiom status`, `axiom pause`, `axiom resume`, `axiom cancel`, `axiom export`.\n")
	b.WriteString("- Use the REST API for lifecycle/read actions and the control WebSocket for privileged orchestrator actions.\n")
	b.WriteString("- Read-only inspection of Axiom artifacts is allowed. Direct implementation outside the engine is not.\n\n")

	b.WriteString("## Axiom Request Types\n\n")
	b.WriteString("- REST endpoints: `/api/v1/projects`, `/api/v1/projects/:id/run`, `/api/v1/projects/:id/srs/approve`, `/api/v1/projects/:id/srs/reject`, `/api/v1/projects/:id/eco/approve`, `/api/v1/projects/:id/eco/reject`, `/api/v1/projects/:id/status`, `/api/v1/projects/:id/tasks`, `/api/v1/projects/:id/costs`, `/api/v1/projects/:id/events`, `/api/v1/models`, `/api/v1/index/query`.\n")
	b.WriteString("- Control request types: `submit_srs`, `submit_eco`, `create_task`, `create_task_batch`, `spawn_meeseeks`, `spawn_reviewer`, `spawn_sub_orchestrator`, `approve_output`, `reject_output`, `query_index`, `query_status`, `query_budget`, `request_inference`.\n")
	b.WriteString("- IPC message types: `task_spec`, `review_spec`, `revision_request`, `task_output`, `review_result`, `inference_request`, `inference_response`, `lateral_message`, `action_request`, `action_response`, `request_scope_expansion`, `scope_expansion_response`, `context_invalidation_warning`, `shutdown`.\n\n")

	b.WriteString("## TaskSpec Rules\n\n")
	b.WriteString("- Every implementation worker receives a self-contained TaskSpec.\n")
	b.WriteString("- Required sections: Base Snapshot, Objective, Context, Interface Contract, Constraints, Acceptance Criteria, Output Format.\n")
	b.WriteString("- Context must follow the minimum-necessary tier system: symbol, file, package, repo-map.\n")
	b.WriteString("- Output goes to `/workspace/staging/` with a manifest describing file operations.\n")
	b.WriteString("- Do not send vague tasks. Every task must trace back to SRS requirements and have explicit acceptance criteria.\n\n")

	b.WriteString("## ReviewSpec Rules\n\n")
	b.WriteString("- ReviewSpec contains the original TaskSpec, the Meeseeks output, automated check results, and review instructions.\n")
	b.WriteString("- Reviewers evaluate correctness, interface contract compliance, obvious bugs, security issues, and code quality.\n")
	b.WriteString("- Review verdicts are `APPROVE` or `REJECT` with criterion-by-criterion reasoning.\n\n")

	b.WriteString("## Budget Rules\n\n")
	b.WriteString(fmt.Sprintf("- Current project budget ceiling: $%.2f\n", g.cfg.Budget.MaxUSD))
	b.WriteString(fmt.Sprintf("- warn threshold: %d%%\n", g.cfg.Budget.WarnAtPercent))
	b.WriteString("- The engine enforces budget on every inference request.\n")
	b.WriteString("- If budget is tight, lower concurrency, prefer cheaper/local models, and reduce retries through Axiom planning rather than bypassing Axiom.\n")
	b.WriteString("- If budget is exhausted, execution must pause and the user must choose whether to raise the budget or cancel.\n\n")

	b.WriteString("## Model Registry Usage\n\n")
	b.WriteString("- Use `axiom models refresh`, `axiom models list`, and `axiom models info` to inspect available models.\n")
	b.WriteString("- Tiers: `local`, `cheap`, `standard`, `premium`.\n")
	b.WriteString("- Choose models based on task size, cost, and historical suitability, not by ad hoc preference.\n\n")

	b.WriteString("## Task Decomposition Principles\n\n")
	b.WriteString("- Create tasks that are appropriately sized, independent where possible, traceable to SRS requirements, test-separated, and coherence-preserving.\n")
	b.WriteString("- Do not split tightly coupled work so far that interfaces become speculative.\n")
	b.WriteString(fmt.Sprintf("- Use the configured branch prefix for engine-managed work: `%s/<project-slug>`.\n\n", g.cfg.Git.BranchPrefix))

	b.WriteString("## Communication Model\n\n")
	b.WriteString("- Default topology is strictly hierarchical: User <-> Engine <-> Orchestrator <-> Sub-orchestrators / Meeseeks / Reviewers.\n")
	b.WriteString("- Meeseeks do not talk directly to each other or to reviewers unless the orchestrator explicitly requests engine-brokered lateral communication.\n")
	b.WriteString("- All lateral communication must stay scoped, logged, and engine-brokered.\n\n")

	b.WriteString("## ECO Flow\n\n")
	b.WriteString("- Valid ECO categories only: `ECO-DEP`, `ECO-API`, `ECO-SEC`, `ECO-PLT`, `ECO-LIC`, `ECO-PRV`.\n")
	b.WriteString("- When an environmental blocker appears, propose an ECO with category, affected SRS sections, environmental issue, and functional substitute.\n")
	b.WriteString("- Never rewrite scope informally. If the ECO is rejected, stay within the approved SRS or stop.\n\n")

	b.WriteString("## Test Authorship Separation\n\n")
	b.WriteString("- test authorship separation is mandatory.\n")
	b.WriteString("- Tests must not be authored by the same Meeseeks that wrote the implementation.\n")
	b.WriteString("- Test generation must be a downstream task run by a different model family.\n")
	b.WriteString("- A feature is not done until implementation and generated tests converge green.\n\n")

	b.WriteString("## Error Handling and Escalation\n\n")
	b.WriteString("- If validation fails, return the failure through Axiom's revision flow instead of patching directly in the runtime.\n")
	b.WriteString("- If repeated failures persist, escalate to replanning or sub-orchestration through the engine.\n")
	b.WriteString("- If the runtime detects instructions that conflict with Axiom, prefer the explicit user instruction to use Axiom and state the conflict clearly.\n")
	return b.String()
}
