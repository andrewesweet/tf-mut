#!/usr/bin/env bash
set -euo pipefail

# The protect hooks must fire only on a tool's own input — never on payload
# metadata such as transcript_path under ~/.claude/projects/, which every call
# carries. The Claude hook asks only when a Write or Edit targets a protected
# file path (.tool_input.file_path); it does not inspect Bash commands or
# written content, so a read-only command naming a protected file, or a write
# elsewhere whose content mentions one, stays silent. The Codex hook cannot
# request permission and instead injects context, so it still inspects the
# whole tool_input for Bash/Edit/Write.

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

claude_ask='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"This operation touches a build-chain, agent-policy, workflow, or lock file. Review it explicitly."}}'
codex_context='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"This operation touches a build-chain, agent-policy, workflow, or lock file. Review it explicitly; Codex PreToolUse hooks cannot request permission."}}'

bash_benign='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Bash","tool_input":{"command":"mise exec -- just test"}}'
bash_justfile='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Bash","tool_input":{"command":"grep -n default Justfile"}}'
edit_settings='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Edit","tool_input":{"file_path":".claude/settings.json","old_string":"a","new_string":"b"}}'
write_github='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Write","tool_input":{"file_path":"README.md","content":"CI lives in .github/workflows/ci.yml"}}'
write_abs_mise='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Write","tool_input":{"file_path":"/repo/mise.toml","content":"x"}}'

expect_silent() {
  local script="$1" payload="$2" label="$3"
  local out
  out=$(jq -cn "$payload" | "$script")
  if [[ -n "$out" ]]; then
    echo "FAIL $label: expected no output, got: $out" >&2
    exit 1
  fi
}

expect_output() {
  local script="$1" payload="$2" expected="$3" label="$4"
  local out
  out=$(jq -cn "$payload" | "$script")
  if [[ "$out" != "$expected" ]]; then
    echo "FAIL $label: unexpected output" >&2
    echo "  want: $expected" >&2
    echo "  got:  $out" >&2
    exit 1
  fi
}

# Claude asks only on a Write/Edit target path: a Bash command that merely
# mentions a protected name, or a write elsewhere whose content mentions one,
# must stay silent.
expect_silent scripts/claude-protect "$bash_benign" \
  "claude-protect: benign bash with .claude transcript_path"
expect_silent scripts/claude-protect "$bash_justfile" \
  "claude-protect: bash command mentions Justfile"
expect_output scripts/claude-protect "$edit_settings" "$claude_ask" \
  "claude-protect: edit targets .claude/settings.json"
expect_silent scripts/claude-protect "$write_github" \
  "claude-protect: write content mentions .github/"
expect_output scripts/claude-protect "$write_abs_mise" "$claude_ask" \
  "claude-protect: write targets absolute /repo/mise.toml"

# Codex cannot ask; it injects context over the whole tool input.
expect_silent scripts/codex-protect "$bash_benign" \
  "codex-protect: benign bash with .claude transcript_path"
expect_output scripts/codex-protect "$bash_justfile" "$codex_context" \
  "codex-protect: bash command mentions Justfile"
expect_output scripts/codex-protect "$edit_settings" "$codex_context" \
  "codex-protect: edit targets .claude/settings.json"
expect_output scripts/codex-protect "$write_github" "$codex_context" \
  "codex-protect: write content mentions .github/"

echo "protect hooks: ok"
