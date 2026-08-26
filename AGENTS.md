# AGENTS.md file

This document describes how to work with the project.
Follow these notes when writing code or submitting pull requests.

## Setup

Only a Go toolchain is required. The project has no third-party modules, so
there is nothing to download and no `go.sum` to keep in step:

```bash
go version
go build ./...
```

To exercise the interoperability checks, install the reference implementation:

```bash
brew install par2          # macOS
apt-get install -y par2    # Debian and Ubuntu
```

## Formatting

Format all code before committing:

```bash
go fmt ./...
gofmt -l .
```

`gofmt -l .` must print nothing. CI fails on any file it lists.

## Testing

Run the full test suite:

```bash
go test -race ./...
```

The encoder fans work out across goroutines, so always run with `-race` rather
than a bare `go test`.

The kernels are architecture specific, and the portable fallback is what runs
everywhere else, so check the other architectures too:

```bash
GOARCH=amd64 go test ./...
GOARCH=386 go build ./...
```

## Coverage

Generate a line-level coverage report:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
go tool cover -html=coverage.out
```

Coverage runs automatically in CI on every push. The coverage summary is
printed in the CI logs for each Go version.

Maintain at least 80% overall line coverage. The `par1` and `par2` packages
should target 90%+ line coverage, since they are the parts that decide whether
somebody's archive survives.

## Linting

Lint all code before committing:

```bash
go vet ./...
```

## Style Guide

- Always update `CHANGELOG.md` according to semantic versioning, mentioning your changes in the unreleased section.
- Write commit messages using [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
- Never bump the `version` constant in `cmd/parchive/main.go`. This is handled by the release process.
- Go files use LF line endings.
- Inline comments should be in the format `// <comment>` and start with uppercase.
- Inline comments should be written as in "Add support for X" rather than "Adds support for X" or "Added support for X".
- Comments should explain why something is done, not restate what the line does.
- Always run the format and testing commands after changes.
- Maintain at least 80% overall line coverage. The `par1` and `par2` packages should target 90%+ line coverage. Run `go tool cover -func=coverage.out` to check.
- Document each package with a package-level doc comment on the file that shares the package name.
- Every exported type, function and constant needs a doc comment starting with its own name.
- Try to avoid single letter variable names, even in short closures - use a bit more descriptive names like `err`, `line`, `part`, etc. The conventional short receivers (`s`, `c`) and loop indices (`i`, `j`) are fine.
- Prefer the standard library. Do not add cgo or third-party modules: staying dependency free and `CGO_ENABLED=0` is the main thing that distinguishes this project.

## On-disk Formats

The formats are the contract, not the Go API. Any change touching packet
layout, field arithmetic or the recovery matrix has to keep interoperating with
the reference tooling in both directions:

```bash
# PAR2, against par2cmdline
parchive create -s8192 -c20 set.par2 data.bin
par2 verify set.par2
par2 repair set.par2

# PAR1, which par2cmdline can repair but not create
parchive create -c4 set.par data.bin
par2 repair set.par
```

The CI workflow runs both round trips on every push. Several tests pin the wire
format directly, and a failure in any of them is a compatibility break rather
than a refactor:

- `par2.TestMulAddMatchesReference` and `par2.TestSIMDMatchesScalar` compare every
  SIMD kernel against a plain multiply. A new kernel is only finished once both
  pass under it.
- `par1.TestGFMatchesPAR1Polynomial` pins GF(2^8) to polynomial `0x11D`.
- `par2.TestRollingMatchesChecksum` pins the rolling CRC32 used by the
  misaligned-data search to `hash/crc32`. Look here first if verification starts
  missing slices it used to find.

## Command Line

The command line is meant to be interchangeable with par2cmdline: the same
options, the same exit codes, and the same recovery files written to disk. The
expectations in `cmd/parchive/layout_test.go` were taken from par2cmdline by
running the same command and listing what it produced, so a change that breaks
them is a compatibility break too.

When touching the command line, compare against a real par2cmdline:

```bash
par2 create -s8192 -c20 set.par2 data.bin && ls
parchive create -s8192 -c20 set.par2 data.bin && ls
```

## Benchmarks

```bash
go test -bench=. -benchmem ./...
go test -bench=MulAddKernel ./par2      # the Reed-Solomon inner loop
go test -bench=Create ./par2            # the whole creation path
```

Do not chase throughput with cgo or third-party modules. Go assembly is
acceptable, and is how the existing kernels are written, as long as the
portable fallback stays and keeps producing identical bytes.

## New Release

To create a new release follow the following steps:

- Make sure that both the tests pass and the code formatting are valid.
- Increment (look at `CHANGELOG.md` for semver changes) the `version` value in `cmd/parchive/main.go`.
- Move all the `CHANGELOG.md` Unreleased items that have at least one non empty item the into a new section with the new version number and date, and then create new empty sub-sections (Added, Changed and Fixed) for the Unreleased section with a single empty item.
- Create a commit with the following message `version: $VERSION_NUMBER`.
- Push the commit.
- Create a new tag with the value of the new version number prefixed with `v`, as in `v$VERSION_NUMBER`. Go modules resolve versions from the tag, so the `v` prefix is required.
- Create a new release on the GitHub repo using the Markdown from the corresponding version entry in `CHANGELOG.md` as the description of the release and the version number as the title. Do not include the title of the release (version and date) in the description.

## License

Parchive-Go is licensed under the [Apache License, Version 2.0](http://www.apache.org/licenses/).
