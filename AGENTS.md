# AGENTS.md file

This document describes how to work with the project.
Follow these notes when writing code or submitting pull requests.

## Setup

Only a Go toolchain is required, the project has no third-party dependencies:

```bash
go version
```

## Formatting

Format all code before committing:

```bash
go fmt ./...
go vet ./...
```

## Testing

Run the full test suite:

```bash
go test -race ./...
```

## Coverage

Generate a line-level coverage report:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

Coverage runs automatically in CI on every push.

Maintain at least 80% overall line coverage. The `par1` and `par2` packages
should target 90%+ line coverage, since they are the parts that decide whether
somebody's archive survives.

## Compatibility

The on-disk formats are the contract, not the Go API. Any change touching
packet layout, field arithmetic or the recovery matrix must keep interoperating
with the reference tooling in both directions:

```bash
# PAR2, against par2cmdline
par2 verify archive.par2
par2 repair archive.par2

# PAR1, against par2cmdline or akalin/gopar
par2 repair archive.par
```

The CI workflow runs the PAR2 round trip on every push. When changing the
field arithmetic, remember that `TestGFMatchesPAR1Polynomial` and the PAR2
constant assignment pin the wire format: if they fail, the change is a
compatibility break rather than a refactor.

## Benchmarks

```bash
go test -bench=. -benchmem ./...
```

Creation is scalar and single threaded by design, to keep the code readable and
dependency free. Do not add cgo, assembly or third-party modules to chase
throughput without discussing it first, it is the main thing that distinguishes
this project.

## Linting

Keep the code `gofmt` clean and `go vet` silent. Comments explain why something
is done, not what the line does.
