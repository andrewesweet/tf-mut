#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
cd "$repo_root"
exec go test ./internal/buildchain/ -run 'Test(NextPatchVersion|Release|WorkflowSecurityContract)' -count=1
