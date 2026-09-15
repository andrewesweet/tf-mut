#!/usr/bin/env bash
set -euo pipefail

# The protect hooks must fire only on a tool's own input — the Bash command
# text, or a Write/Edit file path or content — never on payload metadata such
# as transcript_path under ~/.claude/projects/, which every call carries.

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"

claude_ask='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"This operation touches a build-chain, agent-policy, workflow, or lock file. Review it explicitly."}}'
codex_context='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"This operation touches a build-chain, agent-policy, workflow, or lock file. Review it explicitly; Codex PreToolUse hooks cannot request permission."}}'

bash_benign='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Bash","tool_input":{"command":"mise exec -- just test"}}'
bash_justfile='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Bash","tool_input":{"command":"grep -n default Justfile"}}'
edit_settings='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Edit","tool_input":{"file_path":".claude/settings.json","old_string":"a","new_string":"b"}}'
write_github='{"hook_event_name":"PreToolUse","transcript_path":"/home/agent/.claude/projects/-repo/session.jsonl","tool_name":"Write","tool_input":{"file_path":"README.md","content":"CI lives in .github/workflows/ci.yml"}}'

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

for pair in \
  "scripts/claude-protect $claude_ask" \
  "scripts/codex-protect $codex_context"; do
  read -r script expected <<<"$pair"

  expect_silent "$script" "$bash_benign" "$script: benign bash with .claude transcript_path"
  expect_output "$script" "$bash_justfile" "$expected" "$script: bash command mentions Justfile"
  expect_output "$script" "$edit_settings" "$expected" "$script: edit targets .claude/settings.json"
  expect_output "$script" "$write_github" "$expected" "$script: write content mentions .github/"
done

echo "protect hooks: ok"
