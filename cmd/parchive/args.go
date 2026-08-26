package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// par2cmdline takes options after the command, with the value joined to the
// letter (-s8192, not -s 8192), which is not a shape Go's flag package can
// parse. The parser below follows its rules so that a command line written for
// par2 behaves identically here.

type operation int

const (
	opNone operation = iota
	opCreate
	opVerify
	opRepair
)

type noise int

const (
	noiseSilent noise = iota // -q -q
	noiseQuiet               // -q
	noiseNormal
	noiseNoisy // -v
	noiseDebug // -v -v
)

type config struct {
	op    operation
	noise noise

	archive  string // -a, the recovery set to write or read
	basePath string // -B
	files    []string

	memoryMB uint64 // -m, accepted and reported, this package streams instead

	// verify and repair
	purge      bool // -p
	renameOnly bool // -O
	dataSkip   bool // -N
	skipLeaway int  // -S

	// create
	blockCount        uint32 // -b
	blockSize         uint64 // -s
	blockSizeSet      bool
	redundancy        uint32 // -r
	redundancySet     bool
	redundancySize    uint64 // -r<c><n>
	recoveryBlocks    uint32 // -c
	recoveryBlocksSet bool
	firstBlock        uint32 // -f
	recoveryFiles     uint32 // -n
	scheme            scheme
	schemeSet         bool
	recurse           bool // -R

	showHelp      bool
	showVersion   bool
	showCopyright bool
}

func defaultConfig() *config {
	return &config{
		noise:      noiseNormal,
		blockCount: 2000,
		redundancy: 5,
		skipLeaway: 64,
	}
}

// usageError carries the message par2cmdline would print before exiting with
// eInvalidCommandLineArguments.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func badUsage(format string, a ...any) error {
	return &usageError{fmt.Sprintf(format, a...)}
}

// operationFor recognises the command, including the par2create style names
// that par2cmdline accepts through argv[0].
func operationFor(name string) operation {
	switch strings.ToLower(name) {
	case "c", "create", "par2create":
		return opCreate
	case "v", "verify", "par2verify":
		return opVerify
	case "r", "repair", "par2repair":
		return opRepair
	}
	return opNone
}

func parseArgs(argv0 string, args []string) (*config, error) {
	c := defaultConfig()

	// par2cmdline lets the operation come from the program name, so a symlink
	// called par2repair needs no command word.
	base := strings.TrimSuffix(filepath.Base(argv0), ".exe")
	c.op = operationFor(base)

	i := 0
	if c.op == opNone {
		for i < len(args) {
			a := args[i]
			switch {
			case a == "-h" || a == "--help":
				c.showHelp = true
				return c, nil
			case a == "-V":
				c.showVersion = true
				return c, nil
			case a == "-VV":
				c.showVersion, c.showCopyright = true, true
				return c, nil
			}
			if op := operationFor(a); op != opNone {
				c.op = op
				i++
			}
			break
		}
		if c.op == opNone {
			if len(args) == 0 {
				c.showHelp = true
				return c, nil
			}
			return nil, badUsage("Invalid operation specified: %s", args[0])
		}
	}

	literal := false
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case literal:
			c.files = append(c.files, a)
			continue
		case a == "--":
			literal = true
			continue
		case strings.HasPrefix(a, "@"):
			names, err := readFileList(a[1:])
			if err != nil {
				return nil, err
			}
			c.files = append(c.files, names...)
			continue
		case !strings.HasPrefix(a, "-") || a == "-":
			c.files = append(c.files, a)
			continue
		}
		// -a and -B may be written with the value as the next argument.
		if (a == "-a" || a == "-B") && i+1 < len(args) {
			if a == "-a" {
				c.archive = args[i+1]
			} else {
				c.basePath = args[i+1]
			}
			i++
			continue
		}
		if err := c.option(a); err != nil {
			return nil, err
		}
	}

	if c.showHelp || c.showVersion {
		return c, nil
	}
	if len(c.files) == 0 && c.archive == "" {
		return nil, badUsage("You must specify a Recovery file.")
	}
	return c, c.finish()
}

// option handles one dash argument.
func (c *config) option(a string) error {
	body := a[1:]
	if body == "" {
		return badUsage("Invalid option specified: %s", a)
	}
	letter, rest := body[0], body[1:]

	num := func(what string) (uint64, error) {
		if rest == "" {
			return 0, badUsage("Invalid %s option: %s", what, a)
		}
		v, err := strconv.ParseUint(rest, 10, 64)
		if err != nil {
			return 0, badUsage("Invalid %s option: %s", what, a)
		}
		return v, nil
	}

	switch letter {
	case 'h':
		c.showHelp = true
	case 'V':
		c.showVersion = true
		if rest == "V" {
			c.showCopyright = true
			rest = ""
		}
	case 'a':
		if rest == "" {
			return badUsage("Invalid option specified: %s", a)
		}
		c.archive = rest
		return nil
	case 'B':
		if rest == "" {
			return badUsage("Invalid option specified: %s", a)
		}
		c.basePath = rest
		return nil
	case 'v':
		if c.noise < noiseDebug {
			c.noise++
		}
		return nil
	case 'q':
		if c.noise > noiseSilent {
			c.noise--
		}
		return nil
	case 'm':
		v, err := num("memory")
		if err != nil {
			return err
		}
		c.memoryMB = v
		return nil
	case 'p':
		c.purge = true
		return nil
	case 'O':
		c.renameOnly = true
		return nil
	case 'N':
		c.dataSkip = true
		return nil
	case 'S':
		v, err := num("skip leaway")
		if err != nil {
			return err
		}
		c.skipLeaway = int(v)
		return nil
	case 'b':
		v, err := num("block count")
		if err != nil {
			return err
		}
		if v == 0 || v > 32768 {
			return badUsage("Invalid block count option: %s", a)
		}
		c.blockCount = uint32(v)
		return nil
	case 's':
		v, err := num("block size")
		if err != nil {
			return err
		}
		if v == 0 {
			return badUsage("Invalid block size option: %s", a)
		}
		if v&3 != 0 {
			return badUsage("Block size must be a multiple of 4.")
		}
		c.blockSize, c.blockSizeSet = v, true
		return nil
	case 'r':
		return c.redundancyOption(a, rest)
	case 'c':
		v, err := num("recovery block count")
		if err != nil {
			return err
		}
		if v > 65536 {
			return badUsage("Invalid recovery block count option: %s", a)
		}
		c.recoveryBlocks, c.recoveryBlocksSet = uint32(v), true
		return nil
	case 'f':
		v, err := num("first block")
		if err != nil {
			return err
		}
		c.firstBlock = uint32(v)
		return nil
	case 'u':
		c.scheme, c.schemeSet = schemeUniform, true
		return nil
	case 'l':
		c.scheme, c.schemeSet = schemeLimited, true
		return nil
	case 'n':
		v, err := num("recovery file count")
		if err != nil {
			return err
		}
		if v == 0 || v > 31 {
			return badUsage("Invalid recovery file count option: %s", a)
		}
		if c.scheme == schemeLimited && c.schemeSet {
			return badUsage("Cannot specify limited size and number of files at the same time.")
		}
		// Asking for a specific number of files switches the allocation to
		// uniform, which is what par2cmdline does.
		c.recoveryFiles = uint32(v)
		c.scheme, c.schemeSet = schemeUniform, true
		return nil
	case 'R':
		c.recurse = true
		return nil
	default:
		return badUsage("Invalid option specified: %s", a)
	}
	return nil
}

// redundancyOption accepts a percentage or a target size such as -rm100.
func (c *config) redundancyOption(a, rest string) error {
	if rest == "" {
		return badUsage("Invalid redundancy option: %s", a)
	}
	mult := uint64(0)
	switch rest[0] {
	case 'k', 'K':
		mult = 1 << 10
	case 'm', 'M':
		mult = 1 << 20
	case 'g', 'G':
		mult = 1 << 30
	}
	if mult != 0 {
		v, err := strconv.ParseUint(rest[1:], 10, 64)
		if err != nil {
			return badUsage("Invalid redundancy option: %s", a)
		}
		c.redundancySize = v * mult
		c.redundancy, c.redundancySet = 0, true
		return nil
	}
	v, err := strconv.ParseUint(rest, 10, 64)
	if err != nil || v > 100 {
		return badUsage("Invalid redundancy option: %s", a)
	}
	c.redundancy, c.redundancySet = uint32(v), true
	return nil
}

// finish resolves the recovery file name and the list of input files, and
// rejects the combinations par2cmdline rejects.
func (c *config) finish() error {
	if c.redundancySet && c.recoveryBlocksSet {
		return badUsage("Cannot specify both redundancy and recovery block count.")
	}
	if c.blockSizeSet && c.op == opCreate {
		// -b is ignored once -s is given; par2cmdline treats using both as an
		// error only when both were set explicitly, which we cannot see here,
		// so the explicit size simply wins.
		c.blockCount = 0
	}

	if c.archive == "" {
		if len(c.files) == 0 {
			return badUsage("You must specify a Recovery file.")
		}
		c.archive, c.files = c.files[0], c.files[1:]
	}
	if c.archive == "" {
		return badUsage("failed to set the main par file")
	}

	// A name without the extension gets it appended, and doubles as the source
	// file when no others were named.
	if !strings.EqualFold(filepath.Ext(c.archive), ".par2") && !strings.EqualFold(filepath.Ext(c.archive), ".par") {
		if c.op == opCreate && len(c.files) == 0 {
			c.files = append(c.files, c.archive)
		}
		c.archive += ".par2"
	}

	if c.op == opCreate {
		if c.recurse {
			expanded, err := expandDirs(c.files)
			if err != nil {
				return err
			}
			c.files = expanded
		}
		if len(c.files) == 0 {
			return badUsage("You must specify a list of files when creating.")
		}
		for _, f := range c.files {
			if _, err := os.Stat(f); err != nil {
				return badUsage("You must specify a list of files when creating.")
			}
		}
	}
	return nil
}

// readFileList reads names from a text file, or from standard input for a bare
// "@", one per line.
func readFileList(path string) ([]string, error) {
	in := os.Stdin
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, badUsage("Failed to read filelist: %s", path)
		}
		defer f.Close()
		in = f
	}
	var out []string
	sc := bufio.NewScanner(in)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

// expandDirs replaces directories with the files beneath them, for -R.
func expandDirs(in []string) ([]string, error) {
	var out []string
	for _, name := range in {
		st, err := os.Stat(name)
		if err != nil {
			return nil, err
		}
		if !st.IsDir() {
			out = append(out, name)
			continue
		}
		err = filepath.Walk(name, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
