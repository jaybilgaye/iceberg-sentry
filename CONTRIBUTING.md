# Contributing to Iceberg Sentry

Thanks for the interest — this project is very much open to contributions.

## Ground rules

- **License.** All contributions are made under the [Apache 2.0 license](./LICENSE).
- **Behaviour.** Everyone participating must follow the
  [Code of Conduct](./CODE_OF_CONDUCT.md).
- **Scope.** New features should map to a spec section in
  `Iceberg_Sentry_Spec_v2.md` or an issue we've already discussed. Please
  open an issue before writing anything non-trivial.

## Development setup

```sh
git clone https://github.com/jaybilgaye/iceberg-sentry.git
cd iceberg-sentry

# Build the binary
make build

# Run the full test suite
make test

# Format + vet
make lint
```

Go 1.23+ required (the CI matrix runs on 1.24). No submodules, no
codegen — plain `go build`.

## Generating test fixtures

Realistic integration fixtures are written by pyiceberg:

```sh
pip install "pyiceberg[pyarrow,sql-sqlite]>=0.7.0"
python scripts/gen_fixtures.py --root ./warehouse --clean
```

The `fixtures` namespace contains five tables exercising every Sentry
feature (healthy, streaming, delete-amplification, skewed, PII).

## Adding a new health dimension

1. Add the collector to `internal/scan/scan.go`.
2. Add a dimension function in `internal/health/score.go` and wire it into
   `Score()`.
3. Update the `MaxScore` roll-up in `internal/health/types.go` if needed.
4. Add unit tests in `internal/health/score_test.go` — the pattern is a
   crafted `Stats` struct + assertion on the dimension.
5. Update `site/docs/concepts.html` with the new dimension.
6. Note the change in `CHANGELOG.md` under `Unreleased`.

## Adding a new catalog

1. Create `internal/catalog/<name>/` implementing the
   `catalog.Catalog` interface (`Name`, `LoadTable`, `ListTables`).
2. Wire it into `internal/cli/audit.go` `buildCatalogFromAudit`.
3. Add flags (`--<name>-url`, auth params) to `auditFlags`.
4. Add a unit test — httptest for HTTP catalogs, mock client for gRPC/Thrift.
5. Update `site/docs/catalogs.html`.

## PR checklist

- [ ] `go test -race ./...` passes
- [ ] `go vet ./...` clean
- [ ] `gofmt -w ./` produces no diff
- [ ] `CHANGELOG.md` updated under `Unreleased`
- [ ] Docs updated in `site/docs/` for user-facing changes
- [ ] Commit message body explains the *why*, not just the *what*

## Release process

Releases are cut by pushing a `vX.Y.Z` tag:

```sh
git tag -a v0.4.0 -m "0.4.0"
git push origin v0.4.0
```

GoReleaser runs in CI, produces artifacts for `linux/{amd64,arm64}`,
`darwin/{amd64,arm64}`, `windows/amd64`, uploads them to a GitHub Release
with checksums, and pushes a multi-arch Docker image to
`ghcr.io/jaybilgaye/iceberg-sentry`.

## Questions

Open an issue with the `question` label, or drop into
[GitHub Discussions](https://github.com/jaybilgaye/iceberg-sentry/discussions).
