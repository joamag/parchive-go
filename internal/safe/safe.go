// Package safe resolves the file names carried inside a recovery set against
// the directory being repaired.
//
// Names come out of a .par or .par2 file, which is attacker controlled in every
// realistic threat model: recovery sets travel with the data they protect, and
// repairing one writes to disk. A forged set naming "../../etc/authorized_keys"
// must not be able to place bytes outside the target directory.
package safe

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Join resolves name inside dir, refusing anything that would escape it.
//
// PAR2 uses '/' as its path separator regardless of platform, so the name is
// translated before it is joined. Absolute names, drive letters and any name
// that walks above dir are rejected outright rather than sanitised, because a
// set that names such a file is malformed and silently rewriting the path would
// hide that from the caller.
func Join(dir, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("safe: recovery set contains an empty file name")
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("safe: file name %q contains a NUL byte", name)
	}

	clean := filepath.FromSlash(name)
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, string(filepath.Separator)) {
		return "", fmt.Errorf("safe: file name %q is absolute", name)
	}
	if vol := filepath.VolumeName(clean); vol != "" {
		return "", fmt.Errorf("safe: file name %q names a volume", name)
	}

	path := filepath.Join(dir, clean)

	// filepath.Join cleans the result, so comparing it against the directory is
	// enough to catch every "..", including ones buried mid-path.
	base := filepath.Clean(dir)
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return "", fmt.Errorf("safe: file name %q escapes %q", name, dir)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("safe: file name %q escapes %q", name, dir)
	}
	return path, nil
}
