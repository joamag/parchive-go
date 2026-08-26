package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realFile makes an input file on disk, because parsing a create command
// validates its inputs the same way par2cmdline does.
func realFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "a.bin")
	if err := os.WriteFile(p, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseCommands(t *testing.T) {
	in := realFile(t)
	for _, tc := range []struct {
		args []string
		want operation
	}{
		{[]string{"c", "x.par2", in}, opCreate},
		{[]string{"create", "x.par2", in}, opCreate},
		{[]string{"v", "x.par2"}, opVerify},
		{[]string{"verify", "x.par2"}, opVerify},
		{[]string{"r", "x.par2"}, opRepair},
		{[]string{"repair", "x.par2"}, opRepair},
	} {
		c, err := parseArgs("parchive", tc.args)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if c.op != tc.want {
			t.Fatalf("%v: got operation %d, want %d", tc.args, c.op, tc.want)
		}
	}
}

// par2cmdline takes the operation from the program name when it is invoked
// through one of its aliases, so a symlink needs no command word.
func TestOperationFromProgramName(t *testing.T) {
	in := realFile(t)
	for name, want := range map[string]operation{
		"par2create": opCreate,
		"par2verify": opVerify,
		"par2repair": opRepair,
	} {
		args := []string{"x.par2"}
		if name == "par2create" {
			args = append(args, in)
		}
		c, err := parseArgs("/usr/local/bin/"+name, args)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if c.op != want {
			t.Fatalf("%s: got operation %d, want %d", name, c.op, want)
		}
		if c.archive != "x.par2" {
			t.Fatalf("%s: archive = %q", name, c.archive)
		}
	}
}

func TestParseOptions(t *testing.T) {
	c, err := parseArgs("parchive", []string{
		"create", "-s8192", "-c20", "-f5", "-m512", "-v", "x.par2", realFile(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case c.blockSize != 8192 || !c.blockSizeSet:
		t.Fatalf("block size = %d", c.blockSize)
	case c.recoveryBlocks != 20 || !c.recoveryBlocksSet:
		t.Fatalf("recovery blocks = %d", c.recoveryBlocks)
	case c.firstBlock != 5:
		t.Fatalf("first block = %d", c.firstBlock)
	case c.memoryMB != 512:
		t.Fatalf("memory = %d", c.memoryMB)
	case c.noise != noiseNoisy:
		t.Fatalf("noise = %d", c.noise)
	case c.archive != "x.par2":
		t.Fatalf("archive = %q", c.archive)
	}
}

// -a and -B accept their value joined or as the following argument.
func TestParseSeparatedValues(t *testing.T) {
	for _, args := range [][]string{
		{"verify", "-a", "set.par2", "-B", "/data"},
		{"verify", "-aset.par2", "-B/data"},
	} {
		c, err := parseArgs("parchive", args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if c.archive != "set.par2" || c.basePath != "/data" {
			t.Fatalf("%v: archive=%q basePath=%q", args, c.archive, c.basePath)
		}
	}
}

func TestNoiseLevels(t *testing.T) {
	for _, tc := range []struct {
		flags []string
		want  noise
	}{
		{nil, noiseNormal},
		{[]string{"-q"}, noiseQuiet},
		{[]string{"-q", "-q"}, noiseSilent},
		{[]string{"-q", "-q", "-q"}, noiseSilent},
		{[]string{"-v"}, noiseNoisy},
		{[]string{"-v", "-v"}, noiseDebug},
		{[]string{"-v", "-v", "-v"}, noiseDebug},
	} {
		args := append([]string{"verify"}, tc.flags...)
		c, err := parseArgs("parchive", append(args, "x.par2"))
		if err != nil {
			t.Fatal(err)
		}
		if c.noise != tc.want {
			t.Fatalf("%v: noise = %d, want %d", tc.flags, c.noise, tc.want)
		}
	}
}

// Asking for a number of recovery files switches to uniform sizing.
func TestRecoveryFileCountImpliesUniform(t *testing.T) {
	c, err := parseArgs("parchive", []string{"create", "-n3", "x.par2", realFile(t)})
	if err != nil {
		t.Fatal(err)
	}
	if c.recoveryFiles != 3 || c.scheme != schemeUniform {
		t.Fatalf("files=%d scheme=%d", c.recoveryFiles, c.scheme)
	}
}

func TestRedundancySizes(t *testing.T) {
	in := realFile(t)
	for _, tc := range []struct {
		flag string
		size uint64
		pct  uint32
	}{
		{"-r10", 0, 10},
		{"-rk64", 64 << 10, 0},
		{"-rm1", 1 << 20, 0},
		{"-rg2", 2 << 30, 0},
	} {
		c, err := parseArgs("parchive", []string{"create", tc.flag, "x.par2", in})
		if err != nil {
			t.Fatalf("%s: %v", tc.flag, err)
		}
		if c.redundancySize != tc.size {
			t.Fatalf("%s: size = %d, want %d", tc.flag, c.redundancySize, tc.size)
		}
		if tc.size == 0 && c.redundancy != tc.pct {
			t.Fatalf("%s: redundancy = %d, want %d", tc.flag, c.redundancy, tc.pct)
		}
	}
}

func TestInvalidArguments(t *testing.T) {
	in := realFile(t)
	for _, args := range [][]string{
		{"bogus", "x.par2"},
		{"create", "-s8193", "x.par2", in},
		{"create", "-b0", "x.par2", in},
		{"create", "-n0", "x.par2", in},
		{"create", "-n32", "x.par2", in},
		{"create", "-z", "x.par2", in},
		{"create", "x.par2", "does-not-exist.bin"},
		{"verify"},
	} {
		if _, err := parseArgs("parchive", args); err == nil {
			t.Fatalf("%v should have been rejected", args)
		}
	}
}

// Everything after -- is a filename, even if it starts with a dash.
func TestDoubleDashEndsOptions(t *testing.T) {
	c, err := parseArgs("parchive", []string{"verify", "set.par2", "--", "-weird.bin"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.files) != 1 || c.files[0] != "-weird.bin" {
		t.Fatalf("files = %v", c.files)
	}
}

func TestFileListFromFile(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(list, []byte("one.bin\ntwo.bin\n\nthree.bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := parseArgs("parchive", []string{"verify", "set.par2", "@" + list})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.files, ",") != "one.bin,two.bin,three.bin" {
		t.Fatalf("files = %v", c.files)
	}
}

// A recovery file named without the extension gets it appended, and doubles as
// the input file when no others were given.
func TestArchiveNameGainsExtension(t *testing.T) {
	dir := t.TempDir()
	data := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(data, make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := parseArgs("parchive", []string{"create", data})
	if err != nil {
		t.Fatal(err)
	}
	if c.archive != data+".par2" {
		t.Fatalf("archive = %q, want %q", c.archive, data+".par2")
	}
	if len(c.files) != 1 || c.files[0] != data {
		t.Fatalf("files = %v, want the archive base", c.files)
	}
}

func TestHelpAndVersion(t *testing.T) {
	for _, flag := range []string{"-h", "-V", "-VV"} {
		c, err := parseArgs("parchive", []string{flag})
		if err != nil {
			t.Fatalf("%s: %v", flag, err)
		}
		if !c.showHelp && !c.showVersion {
			t.Fatalf("%s: neither help nor version requested", flag)
		}
	}
}
