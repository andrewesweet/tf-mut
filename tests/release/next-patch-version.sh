#!/usr/bin/env bash
# Exercises scripts/next-patch-version — the automated patch-release decision
# — over throwaway git repositories. The version the merge trigger computes
# for a push to master, the skip when the push carries no change since the
# latest release, and the version the release pipeline re-derives to verify
# an automated version input are all pinned here, before any of them reaches
# a workflow run.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
script=$repo_root/scripts/next-patch-version

sandbox_root=$(mktemp -d "$repo_root/.artifacts/release-tests.XXXXXX")
trap 'rm -rf "$sandbox_root"' EXIT

passed=0
failed=0

commit_env=(-c user.name=Test -c user.email=test@example.invalid -c commit.gpgsign=false)

repo=
new_repo() {
  repo=$(mktemp -d "$sandbox_root/repo.XXXXXX")
  git -C "$repo" init -q --initial-branch=master
  git -C "$repo" "${commit_env[@]}" commit -q --allow-empty -m root
}

new_empty_repo() {
  repo=$(mktemp -d "$sandbox_root/repo.XXXXXX")
  git -C "$repo" init -q --initial-branch=master
}

commit() {
  git -C "$repo" "${commit_env[@]}" commit -q --allow-empty -m change
}

tag() {
  git -C "$repo" tag "$1"
}

annotated_tag() {
  git -C "$repo" "${commit_env[@]}" tag -a "$1" -m "$1"
}

# expect_next description expected_exit expected_stdout runs the default
# mode inside the sandbox repository and compares the exit status and stdout.
expect_next() {
  local description=$1 expected_rc=$2 expected_stdout=${3-}
  local actual rc
  actual=$(cd "$repo" && "$script") && rc=0 || rc=$?
  if [[ $rc -ne $expected_rc ]]; then
    echo "FAIL $description: exit $rc, want $expected_rc" >&2
    failed=$((failed + 1))
    return 0
  fi
  if [[ $actual != "$expected_stdout" ]]; then
    echo "FAIL $description: printed '$actual', want '$expected_stdout'" >&2
    failed=$((failed + 1))
    return 0
  fi
  passed=$((passed + 1))
}

# expect_prev description version expected_exit expected_stdout runs the
# verification mode with the given version and compares exit status and stdout.
expect_prev() {
  local description=$1 version=$2 expected_rc=$3 expected_stdout=${4-}
  local actual rc
  actual=$(cd "$repo" && "$script" --prev "$version") && rc=0 || rc=$?
  if [[ $rc -ne $expected_rc ]]; then
    echo "FAIL $description: exit $rc, want $expected_rc" >&2
    failed=$((failed + 1))
    return 0
  fi
  if [[ $actual != "$expected_stdout" ]]; then
    echo "FAIL $description: printed '$actual', want '$expected_stdout'" >&2
    failed=$((failed + 1))
    return 0
  fi
  passed=$((passed + 1))
}

# expect_usage description argument... asserts the mode arguments are a usage
# error: exit 2 and nothing on stdout.
expect_usage() {
  local description=$1
  shift
  local rc
  (cd "$repo" && "$script" "$@") >/dev/null 2>&1 && rc=0 || rc=$?
  if [[ $rc -ne 2 ]]; then
    echo "FAIL $description: exit $rc, want 2" >&2
    failed=$((failed + 1))
    return 0
  fi
  passed=$((passed + 1))
}

# Default mode: the merge trigger's decision.

new_empty_repo
expect_next "a repository with no commits and no tags is refused" 1 ""

new_repo
expect_next "a repository with no release tag is refused" 1 ""

new_repo
tag v0.1.0
expect_next "a merge destination already carrying the latest release skips" 2 ""

new_repo
tag v0.1.0
commit
expect_next "a commit past the latest release succeeds it" 0 "v0.1.1"

new_repo
tag v0.1.9
tag v0.1.10
commit
expect_next "the latest tag is the greatest version, not the last listed" 0 "v0.1.11"

new_repo
tag v0.1.0
tag v2
commit
expect_next "a non-semantic v* tag is ignored" 0 "v0.1.1"

new_repo
tag v1.2.3
commit
expect_next "a major.minor.patch tag carries on every component" 0 "v1.2.4"

new_repo
tag v2.0.0
commit
expect_next "a zero patch component increments" 0 "v2.0.1"

new_repo
annotated_tag v0.1.0
commit
expect_next "an annotated release tag behaves like a lightweight one" 0 "v0.1.1"

# Verification mode: the release pipeline's --prev re-derivation.

new_repo
tag v0.1.0
commit
tag v0.1.1
expect_prev "the immediate successor of the previous release verifies" v0.1.1 0 "v0.1.1"

expect_prev "a version that skips one is re-derived and does not verify" v0.2.0 0 "v0.1.2"

expect_prev "an untagged future version is re-derived and does not verify" v0.1.9 0 "v0.1.2"

new_repo
tag v0.1.0
expect_prev "re-releasing the first release is refused" v0.1.0 1 ""

new_repo
tag v0.1.9
commit
tag v0.1.10
commit
expect_prev "double digit patches verify in version order" v0.1.11 0 "v0.1.11"

new_repo
tag v0.1.0
expect_prev "a partial version is refused" v0.1 1 ""

expect_prev "a non-version argument is refused" banana 1 ""

new_empty_repo
expect_prev "a repository with no release tag has no predecessor" v0.1.0 1 ""

# Usage errors.

new_repo
expect_usage "an omitted --prev version is a usage error" --prev
expect_usage "an unknown mode is a usage error" --next
expect_usage "a third argument is a usage error" --prev v0.1.0 extra

printf '%d passed, %d failed\n' "$passed" "$failed"
test "$failed" -eq 0
