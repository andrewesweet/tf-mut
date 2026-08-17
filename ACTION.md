# The tf-mut GitHub Action

Mutation testing for `terraform test` on every pull request: SARIF annotations on the exact
changed lines, a maintained PR comment with the score and the new-versus-accepted findings,
and a check whose outcome is tf-mut's own exit code.

```yaml
name: tf-mut
on:
  pull_request:
permissions:
  contents: read
  security-events: write   # SARIF upload
  pull-requests: write     # the maintained comment
jobs:
  mutate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0     # --since needs the base ref's history
      - uses: andrewesweet/tf-mut@v1
        with:
          version: v1.0.0    # always a pinned release; "latest" is refused
```

## The trust and failure-order contract

The composite runs in a fixed order, and the order is the contract:

1. **Install** tf-mut from a versioned release asset for the runner's architecture, verify it
   against the release's published `checksums.txt`, and **fail the Action on any
   verification failure** — nothing unverified executes. The caller supplies the version;
   `latest` is refused.
2. **Run** tf-mut with the documented non-blocking pattern: the run's exit code is captured,
   not obeyed, so the reporting steps always execute.
3. **Upload SARIF** (`security-events: write`).
4. **Create or update the PR comment** (`pull-requests: write`).
5. **Exit with the captured code.** The check outcome is tf-mut's verdict, whatever the
   uploads managed.

## Least-privilege permissions

| Permission | Why |
| --- | --- |
| `contents: read` | Checkout and `--since` history |
| `security-events: write` | The SARIF upload |
| `pull-requests: write` | The maintained comment |

## Fork and Dependabot pull requests

Fork and Dependabot PRs receive a read-only token. The SARIF upload and the comment are
marked `continue-on-error`: they degrade to skipped, and **the check outcome is preserved**
— step 5 still exits with the captured code. Nothing about a token shape can turn a red run
green or a green run red.

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
