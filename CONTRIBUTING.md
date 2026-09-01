# Contributing

## Scope comes first

This exporter exists to provide exactly the three metrics node_exporter
cannot: per-core energy, per-core boost limit, per-socket PROCHOT. Pull
requests adding socket power, temperatures, frequencies, DIMM data,
bandwidth counters or anything GPU will be declined — those belong to
node_exporter's `hwmon`/`rapl`/`cpufreq`/`thermal_zone`/`drm` collectors
or to a GPU exporter. Scope creep is the failure mode this project was
started to avoid.

Dependencies are limited to `github.com/prometheus/client_golang` and
`golang.org/x/sys`; justify any addition explicitly in the PR. No cgo:
`CGO_ENABLED=0 go build` must keep producing a working static binary
(CI enforces this).

Dependabot opens weekly, grouped pull requests for the workflow actions
and the Go modules (`.github/dependabot.yml`). A Go bump rewrites
`go.mod`, `go.sum` and `vendor/` but not the spec's
`Provides: bundled(golang(...))` lines, so CI fails until the PR gains a
commit produced by `scripts/check-bundled-provides.sh --fix`. Dependabot's
own generated commit messages are exempt from the commit-message checks
below.

## Commit messages

Every commit follows [Conventional Commits](https://www.conventionalcommits.org/):
`<type>(<scope>): <description>` with types
`feat fix docs test refactor perf build ci chore revert` and scopes
`hsmp msr topology collector cmd rpm systemd ci docs deps`. Subject in
the imperative mood, lower case after the type, no trailing period,
at most 72 characters. Prefer a bullet-point body — one bullet per
discrete change or rationale — over prose. Breaking changes use `!` and
a `BREAKING CHANGE:` footer.

**Commits must state self-contained facts.** A message has to remain
fully intelligible to someone reading `git log` years from now with no
access to any surrounding context: state *what* changed and *why* in
terms of the code and the system, never in terms of the process that
produced the change. Do not reference conversations, review rounds,
tickets that may become unreachable, relative time ("yesterday", "the
previous commit") or the author's working state. If a bug motivated the
change, describe its observable symptom and mechanism in the body.
Issue/PR numbers may be added as trailers for convenience, but the
message must stand alone if those links die. Commit messages must not
reference AI tooling — no co-author trailers, no generation notes.

CI enforces structure with commitlint (`.commitlintrc.yml`) and the
context rule heuristically with `scripts/check-commit-context.sh`, both
over the full PR commit range. Run the same checks locally per commit by
opting into the shipped hook:

```console
$ git config core.hooksPath .githooks
```

## Merge policy: rebase-merge only

This repository uses **rebase merging exclusively** (repository settings:
*Allow rebase merging* enabled, *Allow squash merging* and *Allow merge
commits* disabled — keep it that way across maintainer changes). Squash
merging would discard the individual commit messages that commit linting
exists to protect; rebase merging replays every commit onto `main`
verbatim, so each linted message survives into permanent history. A PR
title check is therefore deliberately absent — titles never reach
`main`.

The corollary: **every commit in a PR is a public commit** and must
independently satisfy the rules above and build cleanly. Clean up your
branch with an interactive rebase before requesting review; do not append
"fix typo" commits.

## Tests

`gofmt -l` empty, `go vet`, `golangci-lint run` and
`go test -race ./...` must all pass; CI also builds the RPM offline in a
Rocky 9 container and runs the rpmlint policy check
(`scripts/run-rpmlint.sh`). Changes to `internal/hsmp` constants require
kernel-header evidence in the commit body and must keep the ABI
assertions in `hsmp_test.go` passing. Hardware-dependent tests stay
behind the `hsmp_hardware` build tag.

## Releases

Semantic versioning, `CHANGELOG.md` in Keep a Changelog format. A release
is a `vX.Y.Z` tag: the release workflow verifies tag, spec `Version:` and
the newest CHANGELOG entry agree, then builds and attaches the RPM, SRPM,
static-binary tarball and `SHA256SUMS` to the GitHub release.
