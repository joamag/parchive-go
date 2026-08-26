package safe

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestJoinAcceptsOrdinaryNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"movie.mkv",
		"sub/dir/file.bin",
		"a.b.c",
		"ficheiro-português.bin",
		"dots..in..the..middle.bin",
		"..leading-dots.bin",
	} {
		got, err := Join(dir, name)
		if err != nil {
			t.Fatalf("Join(%q) failed: %v", name, err)
		}
		want := filepath.Join(dir, filepath.FromSlash(name))
		if got != want {
			t.Fatalf("Join(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestJoinRejectsEscapes(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"../escaped.txt",
		"../../etc/passwd",
		"sub/../../escaped.txt",
		"a/b/../../../c",
		"..",
	}
	for _, name := range cases {
		if got, err := Join(dir, name); err == nil {
			t.Fatalf("Join(%q) should have been rejected, got %q", name, got)
		}
	}
}

func TestJoinRejectsAbsoluteAndEmpty(t *testing.T) {
	dir := t.TempDir()
	cases := []string{"", "/etc/passwd", "/tmp/x"}
	if runtime.GOOS == "windows" {
		cases = append(cases, `C:\Windows\system32\x`, `\\server\share\x`)
	}
	for _, name := range cases {
		if _, err := Join(dir, name); err == nil {
			t.Fatalf("Join(%q) should have been rejected", name)
		}
	}
}

func TestJoinRejectsNUL(t *testing.T) {
	if _, err := Join(t.TempDir(), "file\x00.bin"); err == nil {
		t.Fatal("a name containing NUL should have been rejected")
	}
}

// PAR2 always uses '/' as its separator, even in sets written on Windows.
func TestJoinTranslatesSlashes(t *testing.T) {
	dir := t.TempDir()
	got, err := Join(dir, "sub/dir/file.bin")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "/") && filepath.Separator != '/' {
		t.Fatalf("Join did not translate separators: %q", got)
	}
}
