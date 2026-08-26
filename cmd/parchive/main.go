// Command parchive creates, verifies and repairs PAR1 and PAR2 recovery sets.
//
//	parchive c(reate) [options] <PAR2 file> [files]
//	parchive v(erify) [options] <PAR2 file> [files]
//	parchive r(epair) [options] <PAR2 file> [files]
//
// The command line, the exit codes and the layout of the files it writes follow
// par2cmdline, so a script written against that tool works unchanged here.
package main

import (
	"fmt"
	"os"
)

// version identifies this client in the creator packet of every set it writes.
const version = "0.1.0"

// Exit codes, matching par2cmdline's Result enum.
const (
	exitSuccess           = 0
	exitRepairPossible    = 1 // damaged, but enough recovery data exists
	exitRepairNotPossible = 2 // damaged, and not enough recovery data exists
	exitInvalidArgs       = 3
	exitInsufficientData  = 4 // the recovery files did not describe the data
	exitRepairFailed      = 5 // repair ran but the files are still wrong
	exitFileIOError       = 6
	exitLogicError        = 7
	exitMemoryError       = 8
)

func main() {
	os.Exit(run(os.Args))
}

func run(argv []string) int {
	cfg, err := parseArgs(argv[0], argv[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInvalidArgs
	}
	switch {
	case cfg.showHelp:
		usage(os.Stdout)
		return exitSuccess
	case cfg.showVersion:
		fmt.Printf("parchive-go version %s\n", version)
		if cfg.showCopyright {
			fmt.Print(copyright)
		}
		return exitSuccess
	}

	switch cfg.op {
	case opCreate:
		return create(cfg)
	case opVerify:
		return check(cfg, false)
	case opRepair:
		return check(cfg, true)
	}
	usage(os.Stderr)
	return exitInvalidArgs
}

const copyright = `
Copyright (c) 2026 Joao Magalhaes.

parchive-go is distributed under the Apache License, Version 2.0.
It implements the Parity Volume Set specifications 1.0 and 2.0.
`

func usage(w *os.File) {
	_, _ = fmt.Fprint(w, `Usage:
  parchive -h  : show this help
  parchive -V  : show version
  parchive -VV : show version and copyright

  parchive c(reate) [options] <PAR2 file> [files] : Create PAR2 files
  parchive v(erify) [options] <PAR2 file> [files] : Verify files using PAR2 file
  parchive r(epair) [options] <PAR2 file> [files] : Repair files using PAR2 files

You may also leave out the "c", "v", and "r" commands by using "par2create",
"par2verify", or "par2repair" instead.

Options: (all uses)
  -a<file> : Set the main PAR2 archive name
  -B<path> : Set the basepath to use as reference for the datafiles
  -v [-v]  : Be more verbose
  -q [-q]  : Be more quiet (-q -q gives silence)
  -m<n>    : Memory (in MB) to use
  --       : Treat all following arguments as filenames
Options: (verify or repair)
  -p       : Purge backup files and par files on successful recovery or
             when no recovery is needed
  -N       : Data skipping (find badly mispositioned data blocks)
  -S<n>    : Skip leaway (distance +/- from expected block position, default 64)
Options: (create)
  -b<n>    : Set the Block-Count (default 2000)
  -s<n>    : Set the Block-Size (don't use both -b and -s)
  -r<n>    : Level of redundancy (%, default 5%)
  -r<c><n> : Redundancy target size, <c>=g(iga),m(ega),k(ilo) bytes
  -c<n>    : Recovery Block-Count (don't use both -r and -c)
  -f<n>    : First Recovery-Block-Number (default 0)
  -u       : Uniform recovery file sizes (default is variable)
  -l       : Limit size of recovery files (don't use both -u and -l)
  -n<n>    : Number of recovery files (max 31) (don't use both -n and -l)
  -R       : Recurse into subdirectories
             (Be aware of wildcard shell expansion)
   @       : Process a listing of files specified in text (file) input
             (eg. @filelist.txt, or bare @ to read from stdin)

Example:
   parchive repair *.par2
`)
}

// out prints at or above the given noise level, which is how the -v and -q
// options take effect.
func (c *config) out(level noise, format string, a ...any) {
	if c.noise >= level {
		fmt.Printf(format, a...)
	}
}
