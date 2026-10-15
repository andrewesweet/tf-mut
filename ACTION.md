# The tf-mut GitHub Action

Mutation testing for `terraform test` on every pull request: SARIF annotations on the exact
changed lines, the markdown summary — score and new-versus-accepted findings — in the job
step summary, and a check whose outcome is tf-mut's own exit code. (Adoption's permission
ask is minimal: the step summary needs no token at all. The maintained PR comment this
summary replaced was removed in M4 —
[issue #64](https://github.com/andrewesweet/tf-mut/issues/64) records the change.)

Add the workflow below to `.github/workflows/tf-mut.yml` of the repository whose Terraform
modules it should mutate:

```yaml
name: tf-mut
on:
  pull_request:
permissions:
  contents: read
  security-events: write   # SARIF upload
jobs:
  mutate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0     # --since needs the base ref's history
      - uses: hashicorp/setup-terraform@v4
        with:
          terraform_wrapper: false
      - uses: andrewesweet/tf-mut@v0.1.0
        with:
          version: v0.1.0    # always a pinned release; "latest" is refused
```

1. **Install Terraform.** Terraform is a prerequisite: the Action installs tf-mut only.
   Run `hashicorp/setup-terraform` before tf-mut with `terraform_wrapper: false` so tf-mut
   invokes the Terraform binary directly.
2. **Choose the version to pin.** The `uses:` ref and the `version:` input name the same
   released `v*` tag of this repository. `latest` is refused: an unpinned tool in CI is an
   unverified tool.
3. **Grant the two permissions.** The job needs `contents: read` for checkout and `--since`
   history, and `security-events: write` for the SARIF upload; the step summary needs no
   permission at all.
4. **Read the result.** The markdown summary lands in the job step summary. The check's
   outcome is tf-mut's own exit code, captured before any upload ran: `1` means findings —
   mutants survived — and fails the check; `2` is an operational failure. The reporting
   steps run before the final exit step. SARIF upload failures do not change that exit code;
   see [Fork and Dependabot pull requests](#fork-and-dependabot-pull-requests).

## The trust and failure-order contract

The composite runs in a fixed order, and the order is the contract:

1. **Install** tf-mut from a versioned release asset for the runner's architecture, verify it
   against the release's published `checksums.txt`, and **fail the Action on any
   verification failure** — nothing unverified executes. The caller supplies the version;
   `latest` is refused.
2. **Run** tf-mut with the documented non-blocking pattern: the run's exit code is captured,
   not obeyed, so the reporting steps always execute.
3. **Upload SARIF** (`security-events: write`). The run step has already written the
   markdown summary to the job step summary, which needs no permission.
4. **Exit with the captured code.** The check outcome is tf-mut's verdict, whatever the
   upload managed.

## Least-privilege permissions

| Permission | Why |
| --- | --- |
| `contents: read` | Checkout and `--since` history |
| `security-events: write` | The SARIF upload |

## Fork and Dependabot pull requests

Fork and Dependabot PRs receive a read-only token. The SARIF upload is marked
`continue-on-error`: whatever a token refuses degrades, and **the check outcome is
preserved** — the final step still exits with the captured code. Nothing about a token
shape can turn a red run green or a green run red. The step summary is unaffected by token
shape entirely.

What degrades is measured, not assumed: on pull-request events GitHub accepts the
SARIF upload from a read-only token — the platform scopes that write itself, so annotations
survive even on fork PRs. On non-PR events a read-only token refuses the SARIF upload.

## `pull_request_target` is forbidden

Never run this Action from a `pull_request_target` workflow against an untrusted checkout.
That event grants a write token while executing the fork's code — the exact confused-deputy
shape this Action's degradation contract exists to avoid. Use `pull_request`, accept the
read-only degradation on forks, and keep the write-token surface at zero.

## Distribution

Releases ship one archive per architecture (`tf-mut_<version>_linux_amd64.tar.gz`,
`tf-mut_<version>_linux_arm64.tar.gz`) with a `checksums.txt` published alongside in the
same release. The Action downloads the caller's pinned version, verifies the archive against
the checksums file, and fails on any mismatch. The `download-base-url` input exists for
mirrors and for this repository's own workflow test; checksum verification applies wherever
the assets come from.

## Credentials

The Action requires no *infrastructure-provider* credentials: tf-mut refuses unmocked
providers without an explicit opt-in, and a fully-mocked suite reaches no cloud. The GitHub
token permissions above are the whole surface.
