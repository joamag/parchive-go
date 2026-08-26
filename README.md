<div align="center">
  <img src="res/logo.svg" alt="parchive-go" width="220" />

  **Simple (yet complete) PAR1 and PAR2 recovery sets in pure Go 🛟**
</div>

## Warning

**parchive-go has been written for educational purposes and shouldn't be taken too seriously.** Use it at your own risk!

## Description

Built on top of the powerful [Go Programming Language](https://go.dev), parchive-go implements the [PAR1](https://parchive.github.io/doc/Parity%20Volume%20Set%20Specification%20v1.0.html) and [PAR2](https://parchive.sourceforge.net/docs/specifications/parity-volume-spec/article-spec.html) formats end to end: packet serialisation, Reed-Solomon coding over GF(2^16) for PAR2 and GF(2^8) for PAR1, and the creation, verification and repair of recovery sets.

Parity files sit next to the data they protect. When bit rot flips a byte, a download truncates, or a file disappears altogether, the recovery volumes hold enough redundancy to rebuild what was lost, byte for byte. It is the trick Usenet has relied on for two decades, and it is just as useful for optical media, tape and cold archives.

The whole thing is around 2,200 lines across two packages. No cgo, no third-party modules, no vendored binaries: just the standard library, so it cross-compiles anywhere Go does. Roughly 170 of those lines are Go assembly, the SIMD kernels for arm64 and amd64, and every architecture without one falls back to a portable Go path that produces identical bytes.

### Features

- Full PAR2 support: create, verify and repair, reading and writing the real on-disk format
- Full PAR1 support, including the UTF-16 file names that format stores natively
- Reed-Solomon over GF(2^16) with the PAR2-mandated generator 2 and polynomial `0x1100B`
- Reed-Solomon over GF(2^8) with polynomial `0x11D` for PAR1
- Resynchronising packet scanner that skips damaged packets, so a partly corrupt `.par2` still works
- Per-slice CRC32 and MD5 verification, matching how the format detects damage
- Misaligned data recovery: a rolling checksum finds slices that moved because bytes were inserted or deleted, and slices that ended up in a different file
- Repair by Gauss-Jordan elimination, rebuilding damaged slices and fully missing files alike
- SIMD encoding on arm64 (NEON) and amd64 (SSSE3), with a portable fallback that is verified byte for byte against it
- Single pass over the input: hashing and encoding read the same bytes, concurrently
- Memory bounded by slice size times recovery count, not by the size of the protected data
- Command line compatible with par2cmdline: same options, same exit codes, same recovery files on disk
- Usable as a library or as a single self-contained `parchive` binary
- Zero third-party dependencies, `CGO_ENABLED=0`, every `GOARCH` Go supports

## Why another Parchive implementation?

The reference implementation, [par2cmdline](https://github.com/Parchive/par2cmdline), is excellent and very much alive - it shipped five releases in the sixteen months to August 2026. It is also GPL-2.0 and a command-line tool, which means a permissively licensed Go program cannot link it and has to fork a subprocess instead, sniffing `par2 -h` output to work out which flags the installed version understands. Debian stable still ships 0.8.1 while testing carries 1.3.0, so that subprocess behaves differently from host to host.

The Go side has been thin for a long time. [klauspost/reedsolomon](https://github.com/klauspost/reedsolomon), the de facto erasure-coding library, [declined to add a PAR2-compatible GF(2^16) engine](https://github.com/klauspost/reedsolomon/issues/72): *"I do not plan to add another since it will be inferior."* The closest thing to an incumbent, [akalin/gopar](https://github.com/akalin/gopar), covers both formats but has been dormant since 2021.

parchive-go does not try to be faster or more complete than par2cmdline. It aims to be the version you can simply `import`.

| | parchive-go | [akalin/gopar](https://github.com/akalin/gopar) | [par2cmdline](https://github.com/Parchive/par2cmdline) | [par2cmdline-turbo](https://github.com/animetosho/par2cmdline-turbo) |
| --- | --- | --- | --- | --- |
| Language | Go | Go | C++ | C++ |
| Licence | Apache-2.0 | BSD-3-Clause | GPL-2.0 | GPL-2.0 |
| PAR1 | `Yes` | `Yes` | `Repair only` | `Repair only` |
| PAR2 | `Yes` | `Yes` | `Yes` | `Yes` |
| Importable library | `Yes` | `Yes` | `Coarse (libpar2)` | `Coarse (libpar2)` |
| Third-party dependencies | `None` | `3` | `n/a` | `n/a` |
| Needs cgo | `No` | `No` | `n/a` | `n/a` |
| SIMD acceleration | `arm64, amd64` | `amd64 only` | `Yes` | `Yes` |
| Misaligned data recovery | `Yes` | `Yes` | `Yes` | `Yes` |
| par2cmdline compatible CLI | `Yes` | `Partial` | `Reference` | `Yes` |
| Maintained | `Yes` | `Dormant since 2021` | `Yes` | `Yes` |

Where parchive-go genuinely differs is narrow and honest: it is the only Go implementation that creates *and* verifies *and* repairs both formats with no third-party modules and no cgo, under a permissive licence, in a codebase small enough to read in an afternoon. It is not the first pure-Go PAR2 library and it is not the fastest anything.

## Compatibility

Interoperability is the point, so it is tested rather than asserted. Every claim below was verified by round trip, in both directions, with the files compared byte for byte afterwards:

- **PAR2 against [par2cmdline-turbo](https://github.com/animetosho/par2cmdline-turbo) 1.5.0** - volumes written by parchive-go repair damaged and deleted files under `par2`, and parchive-go repairs sets that `par2` created
- **PAR1 against [akalin/gopar](https://github.com/akalin/gopar)** - the same round trip in both directions, including a file reconstructed from nothing
- **PAR1 against [par2cmdline](https://github.com/Parchive/par2cmdline) 1.3.0** - the reference implementation repairs PAR1 sets written by parchive-go, rebuilding a corrupted file and a deleted one in a single pass

The CI workflow runs the PAR2 half of this against the `par2` package on every push, so a regression breaks the build rather than someone's archive.

## Installation

Install the command line tool:

```bash
go install github.com/joamag/parchive-go/cmd/parchive@latest
```

Or add the library to a project:

```bash
go get github.com/joamag/parchive-go
```

Building from a checkout needs nothing but a Go toolchain:

```bash
git clone https://github.com/joamag/parchive-go.git
cd parchive-go
go build ./...
```

## Usage

The command line follows [par2cmdline](https://github.com/Parchive/par2cmdline): the same commands, the same options, the same exit codes, and the same recovery files on disk. A script written against `par2` works here by changing the program name.

<img src="res/demo.svg" alt="Creating, damaging, verifying and repairing a recovery set" width="760" />

```text
parchive c(reate) [options] <PAR2 file> [files]
parchive v(erify) [options] <PAR2 file> [files]
parchive r(epair) [options] <PAR2 file> [files]
```

Options come after the command, and their values are joined to the letter: `-s4096`, not `-s 4096`. The recovery file is the first name that is not an option; anything after it is an input file. A name given without an extension gains `.par2`, and doubles as the input file when no others are named, so `parchive create movie.mkv` protects `movie.mkv` with `movie.mkv.par2`.

Symlinking the binary to `par2create`, `par2verify` or `par2repair` takes the operation from the program name, as par2cmdline does.

### Commands

| Command | Aliases | What it does |
| --- | --- | --- |
| `create` | `c`, `par2create` | Compute recovery data for the given files and write a recovery set |
| `verify` | `v`, `par2verify` | Check the files a recovery set describes and report what is wrong |
| `repair` | `r`, `par2repair` | Verify, then rebuild whatever is damaged or missing |

### Options for every command

| Option | Default | Meaning |
| --- | --- | --- |
| `-a<file>` | first filename | Name of the main recovery file. May also be written `-a <file>` |
| `-B<path>` | the recovery file's directory | Directory the data files are resolved against |
| `-v`, `-v -v` | off | More detail; twice adds per-file diagnostics |
| `-q`, `-q -q` | off | Less detail; twice is silence |
| `-m<n>` | half of RAM | Memory budget in MB. Accepted for compatibility; this implementation streams, so its footprint is set by the block size and count |
| `--` | | Everything after this is a filename, even if it starts with a dash |
| `@<file>` | | Read filenames from a text file, one per line. A bare `@` reads standard input |
| `-h` / `-V` / `-VV` | | Help, version, version with copyright |

### Options for `create`

| Option | Default | Meaning |
| --- | --- | --- |
| `-b<n>` | `2000` | Split the input into about this many blocks |
| `-s<n>` | derived from `-b` | Block size in bytes, a multiple of 4. Takes precedence over `-b` |
| `-r<n>` | `5` | Redundancy as a percentage of the input |
| `-r<c><n>` | | Redundancy as a target size: `-rk64`, `-rm100`, `-rg2` |
| `-c<n>` | derived from `-r` | Exact number of recovery blocks. Do not combine with `-r` |
| `-f<n>` | `0` | Exponent the first recovery block gets, for extending an existing set |
| `-u` | off | Give every recovery file the same size |
| `-l` | off | Cap recovery file size at the largest input file |
| `-n<n>` | about log2 of the block count | Number of recovery files, at most 31. Implies `-u` |
| `-R` | off | Recurse into any directory named on the command line |

Block size and redundancy are two ways of asking the same question. `-b` and `-r` describe what you want in relative terms and let the tool work out the rest; `-s` and `-c` state the numbers outright. A larger block size means fewer, coarser blocks: cheaper bookkeeping, but each damaged byte costs a bigger block to repair.

### Options for `verify` and `repair`

| Option | Default | Meaning |
| --- | --- | --- |
| `-p` | off | Delete the recovery files once the data is known to be good |
| `-N` | off | Tolerate unrecognised data while searching. Accepted for compatibility; the search for misplaced blocks always runs here |
| `-S<n>` | `64` | How far from its expected position a block may be found. Accepted for compatibility |

### Examples

Protect a file with the defaults, which is 5% redundancy over about 2000 blocks. The recovery file is named after the input, so nothing else needs saying:

```bash
parchive create movie.mkv
```

```text
movie.mkv.par2  movie.mkv.vol000+01.par2  movie.mkv.vol001+02.par2
movie.mkv.vol003+04.par2  movie.mkv.vol007+08.par2  movie.mkv.vol015+16.par2
movie.mkv.vol031+32.par2  movie.mkv.vol063+37.par2
```

Protect several files with an explicit block size and count:

```bash
parchive create -s4096 -c20 archive.par2 photo.raw notes.tar
```

```text
Block size: 4096
Source file count: 2
Source block count: 15
Recovery block count: 20
Recovery file count: 5
```

The recovery blocks are spread across exponentially sized volumes, so a small amount of damage only needs a small download:

```text
archive.par2  archive.vol00+1.par2  archive.vol01+2.par2
archive.vol03+4.par2  archive.vol07+8.par2  archive.vol15+5.par2
```

Ten percent redundancy in three equally sized recovery files, recursing into a directory:

```bash
parchive create -r10 -n3 -R backup.par2 photos/
```

```text
backup.par2  backup.vol000+67.par2  backup.vol067+67.par2  backup.vol134+66.par2
```

Check a set:

```bash
parchive verify archive.par2
```

```text
Verifying source files:

Target: "notes.tar" - damaged. Found 3 of 5 data blocks.
Target: "photo.raw" - damaged. Found 10 of 10 data blocks.

Repair is required.
2 file(s) exist but are damaged.
You have 13 out of 15 data blocks available.
You have 20 recovery blocks available.
Repair is possible.
2 recovery blocks will be used to repair.
```

`photo.raw` there had a byte inserted at the front. Every block moved off its offset, yet all ten were still found, so putting it right costs no recovery data. Only the two blocks genuinely lost from `notes.tar` need parity.

Put it right, then clear the recovery files away:

```bash
parchive repair -p archive.par2
```

For a PAR1 set, name the recovery file `.par`. par2cmdline can repair those but not create them, which is the one place the two tools differ in what they accept:

```bash
parchive create -c5 archive.par photo.raw notes.tar
```

### Exit codes

The values par2cmdline defines, so wrapper scripts behave the same:

| Code | Meaning |
| --- | --- |
| `0` | Success, or nothing needed repairing |
| `1` | Files are damaged and there is enough recovery data to repair them |
| `2` | Files are damaged and there is not enough recovery data |
| `3` | Something was wrong with the command line |
| `4` | The recovery files did not describe the data files |
| `5` | Repair ran but the files are still damaged |
| `6` | A file could not be read or written |

### Library

Creating a recovery set:

```go
package main

import (
    "log"
    "os"

    "github.com/joamag/parchive-go/par2"
)

func main() {
    set, err := par2.Create([]string{"photo.raw", "notes.tar"}, 4096, 0, 20, "myapp")
    if err != nil {
        log.Fatal(err)
    }

    index, err := os.Create("archive.par2")
    if err != nil {
        log.Fatal(err)
    }
    defer index.Close()

    if err := set.WriteIndex(index); err != nil {
        log.Fatal(err)
    }
}
```

Verifying and repairing an existing one:

```go
set, err := par2.Parse("archive.par2", "archive.vol000+20.par2")
if err != nil {
    log.Fatal(err)
}

status, err := set.Verify(".")
if err != nil {
    log.Fatal(err)
}
for _, st := range status {
    if !st.OK {
        log.Printf("%s needs %d slices", st.File.Name, len(st.Damaged))
    }
}

if err := set.Repair("."); err != nil {
    log.Fatal(err)
}
```

The `par1` package mirrors this shape, with `par1.Create`, `par1.Parse`, `Verify` and `Repair`.

## How it works

<img src="res/pipeline.svg" alt="How parchive-go creates, verifies and repairs a recovery set" width="820" />

Both formats build on the same idea: treat the data as a matrix over a finite field and store enough extra rows that any missing rows can be solved for.

PAR2 chops every input file into equal slices and numbers them globally. Each input slice `i` gets a constant `c_i`, chosen as `2^k` for successive `k` coprime with 65535 so that every constant has full multiplicative order. Recovery slice `e` is then the sum over all input slices of `c_i^e` times the slice data, computed in GF(2^16) with the polynomial `0x1100B`. Alongside the parity, the format records a CRC32 and an MD5 per slice, which is what makes verification cheap and damage detection precise.

Repair subtracts the surviving slices from the recovery slices, leaving a linear system whose unknowns are exactly the damaged slices. Gauss-Jordan elimination over the same field solves it, and the results are written back into place.

PAR1 is the older and blunter design: each *file* is one shard, zero padded to the length of the largest, and recovery volume `v` holds the sum over files of `(i+1)^(v-1)` times the file data in GF(2^8). Losing a 4 KiB file costs a whole volume the size of the biggest file in the set, which is precisely why PAR2 replaced it.

## Finding data that moved

Damage is not always a flipped byte. A truncated download, a botched concatenation or an editor that rewrote a file can insert or delete bytes, and from then on every slice sits at the wrong offset even though the data is perfectly intact. Checking the offsets a slice is supposed to be at would call all of it lost.

PAR2 records a CRC32 per slice, which makes those slices findable: slide a window one slice wide across the file and watch for a checksum that belongs to the set. Recomputing a CRC at every byte would be quadratic, so the window is updated incrementally instead. The IEEE CRC register update is affine over GF(2), which means the register for the next window is the current one advanced by the incoming byte, with the outgoing byte's contribution removed:

```text
C' = step(C, incoming) ^ A^n(table[outgoing])
```

where `A` is the linear map that advances the register by one zero byte. `A^n` for a window of megabytes comes from repeated squaring of a 32 by 32 bit matrix rather than iterating, so the precomputation is instant regardless of slice size.

The scan only runs when the cheap aligned check already came up short, so intact data never pays for it. Within a scan, a window that matches a known slice is stepped over rather than crawled through, and files that verified cleanly are not searched at all. Library callers that would rather fail fast can turn it off with `Options{NoScan: true}`.

## Limitations

Stated plainly, because a data-integrity tool that oversells itself is worse than useless:

- **No AVX2 or AVX-512 kernel.** amd64 uses SSSE3, which every x86-64 processor since 2006 has, but a wider kernel would go faster still. Verification and repair are also less parallel than creation.
- **Repair holds the damaged file in memory** while it is rewritten, so a single file larger than RAM will not repair.
- **No subdirectories.** File names are taken as base names on creation.
- **No PAR2 Unicode Filename packets.** Non-ASCII names work in PAR1, which stores UTF-16 natively, but PAR2 sets use the ASCII name packet only.
- **No PAR3.** The specification is still unfinished.

## Benchmarks

Measured on an Apple M-series laptop, 256 MiB input, 512 KiB slices, 20 recovery slices, best of three runs:

| | parchive-go | akalin/gopar | Parchive par2cmdline 1.3.0 | par2cmdline-turbo 1.5.0 |
| --- | --- | --- | --- | --- |
| Create | `0.37s` | `0.99s` | `1.92s` | `0.36s` |
| Verify | `0.37s` | `0.74s` | `1.25s` | `0.33s` |
| Repair | `1.11s` | `1.25s` | `2.79s` | `1.15s` |
| Peak memory, create | `38 MiB` | `379 MiB` | `13 MiB` | `30 MiB` |

Creation runs level with par2cmdline-turbo, the SIMD-accelerated C++ implementation, and repair edges ahead of it. Against the plain reference implementation it is five times faster to create and roughly two and a half times faster to repair. The repair figure includes the misaligned-data search, which par2cmdline also performs.

Three things got it there, in order of how much they mattered. The Reed-Solomon inner loop moved from a log-add-antilog sequence with a modulo per value to nibble tables driven by one SIMD instruction, which took it from 1.5 GB/s to 20 GB/s. The input is now read once instead of twice, with the whole-file hash, the per-slice checksums and the encoding all consuming the same buffer concurrently. And the recovery slices are partitioned across cores, which is safe because no two of them ever touch the same output buffer.

The memory column is worth dwelling on: gopar loads every input file into memory, so its footprint tracks the size of the archive, while parchive-go's tracks slice size times recovery count plus a small read-ahead buffer.

Reproduce the inner loop with `go test -bench=MulAddKernel ./par2`, or the whole operation with `go test -bench=Create ./par2`.

## Security

Repairing a recovery set means writing files whose names came out of that set, and a `.par2` file travels with the data it protects. parchive-go rejects any file name that is absolute or that escapes the directory being repaired, rather than trusting it: the same class of issue as [GHSA-j5pc-g362-c5xp](https://github.com/Parchive/par2cmdline/security/advisories/GHSA-j5pc-g362-c5xp) in par2cmdline.

That guard is the only hardening claim made here. Repair still writes in place, so keep backups of anything irreplaceable.

## License

parchive-go is currently licensed under the [Apache License, Version 2.0](http://www.apache.org/licenses/).

## Build Automation

[![Build Status](https://github.com/joamag/parchive-go/workflows/Main%20Workflow/badge.svg)](https://github.com/joamag/parchive-go/actions)
[![Go Reference](https://pkg.go.dev/badge/github.com/joamag/parchive-go.svg)](https://pkg.go.dev/github.com/joamag/parchive-go)
[![Go Version](https://img.shields.io/github/go-mod/go-version/joamag/parchive-go)](https://go.dev)
[![Dependencies](https://img.shields.io/badge/dependencies-none-brightgreen)](go.mod)
[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](https://www.apache.org/licenses/)
