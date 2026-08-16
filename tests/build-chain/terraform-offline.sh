#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
terraform_config="$repo_root/.tools/terraform-cli.tfrc"
provider_lock="$repo_root/research/spikes/fixture-b/.terraform.lock.hcl"

test -f "$terraform_config"
test -f "$provider_lock"
if git check-ignore -q "$provider_lock"; then
  echo "provider lock file must be tracked" >&2
  exit 1
fi

export TF_CLI_CONFIG_FILE="$terraform_config"
export CHECKPOINT_DISABLE=1
export HTTP_PROXY=http://127.0.0.1:1
export HTTPS_PROXY=http://127.0.0.1:1
export NO_PROXY=

sandbox_root=$(mktemp -d "$repo_root/.artifacts/terraform-tests.XXXXXX")
trap 'rm -rf "$sandbox_root"' EXIT

for fixture in fixture-a fixture-b fixture-c; do
  fixture_dir="$sandbox_root/$fixture"
  mkdir -p "$fixture_dir"
  cp -a "$repo_root/research/spikes/$fixture/." "$fixture_dir/"

  terraform -chdir="$fixture_dir" init \
    -backend=false -input=false -lockfile=readonly -no-color
  terraform -chdir="$fixture_dir" test -no-color
done
