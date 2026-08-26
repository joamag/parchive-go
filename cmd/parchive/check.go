package main

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joamag/parchive-go/par1"
	"github.com/joamag/parchive-go/par2"
)

func check(c *config, repair bool) int {
	if strings.EqualFold(filepath.Ext(c.archive), ".par") {
		return checkPar1(c, repair)
	}
	return checkPar2(c, repair)
}

// companions lists the recovery files belonging to a set, the way par2cmdline
// picks up every volume sitting next to the one it was given.
func companions(archive string) []string {
	base := strings.TrimSuffix(archive, filepath.Ext(archive))
	found, _ := filepath.Glob(base + "*.par2")
	sort.Strings(found)

	out := []string{archive}
	for _, f := range found {
		if f != archive {
			out = append(out, f)
		}
	}
	return out
}

// loadReport prints the per-file packet tally par2cmdline shows while reading a
// recovery set, counting a packet as new the first time its contents are seen.
func loadReport(c *config, paths []string) {
	seen := map[[16]byte]bool{}
	for _, p := range paths {
		c.out(noiseQuiet, "Loading \"%s\".\n", p)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		packets, recovery := 0, 0
		for _, pk := range par2.ReadPackets(data) {
			h := md5.New()
			h.Write(pk.SetID[:])
			h.Write(pk.Type[:])
			if pk.Type == par2.TypeRecovery && len(pk.Body) >= 4 {
				h.Write(pk.Body[:4]) // the exponent identifies a recovery packet
			} else {
				h.Write(pk.Body)
			}
			var key [16]byte
			copy(key[:], h.Sum(nil))
			if seen[key] {
				continue
			}
			seen[key] = true
			packets++
			if pk.Type == par2.TypeRecovery {
				recovery++
			}
		}
		if packets == 0 {
			c.out(noiseNormal, "No new packets found\n")
			continue
		}
		if recovery > 0 {
			c.out(noiseNormal, "Loaded %d new packets including %d recovery blocks\n", packets, recovery)
		} else {
			c.out(noiseNormal, "Loaded %d new packets\n", packets)
		}
	}
}

func checkPar2(c *config, repair bool) int {
	dir := c.basePath
	if dir == "" {
		dir = filepath.Dir(c.archive)
	}

	paths := companions(c.archive)
	loadReport(c, paths)

	set, err := par2.Parse(paths...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInsufficientData
	}

	total := set.TotalSlices()
	var totalSize uint64
	for _, fd := range set.Files {
		totalSize += fd.Size
	}
	c.out(noiseNormal, "\nThere are %d recoverable files and 0 other files.\n"+
		"The block size used was %d bytes.\n"+
		"There are a total of %d data blocks.\n"+
		"The total size of the data files is %d bytes.\n",
		len(set.Files), set.SliceSize, total, totalSize)
	c.out(noiseNormal, "\nVerifying source files:\n\n")

	// par2cmdline searches for misplaced blocks by default, and reports the
	// files sorted by name rather than in the order the main packet lists them.
	status, err := set.Verify(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}
	sorted := append([]par2.FileStatus(nil), status...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].File.Name < sorted[j].File.Name })

	complete, damaged, missing, have := 0, 0, 0, 0
	for _, st := range sorted {
		n := len(st.File.Slices)
		good := n - len(st.Damaged)
		have += good
		switch {
		case st.OK:
			c.out(noiseQuiet, "Target: \"%s\" - found.\n", st.File.Name)
			complete++
		case !st.Present && len(st.Damaged) == n:
			c.out(noiseQuiet, "Target: \"%s\" - missing.\n", st.File.Name)
			missing++
		default:
			c.out(noiseQuiet, "Target: \"%s\" - damaged. Found %d of %d data blocks.\n",
				st.File.Name, good, n)
			damaged++
		}
	}
	need := total - have
	if complete < len(set.Files) {
		c.out(noiseNormal, "\nScanning extra files:\n\n")
	}
	if complete == len(set.Files) && need == 0 {
		c.out(noiseQuiet, "\nAll files are correct, repair is not required.\n")
		if repair && c.purge {
			purge(c, set, dir)
		}
		return exitSuccess
	}

	available := len(set.Recovery)
	c.out(noiseQuiet, "\nRepair is required.\n")
	if missing > 0 {
		c.out(noiseNormal, "%d file(s) are missing.\n", missing)
	}
	if damaged > 0 {
		c.out(noiseNormal, "%d file(s) exist but are damaged.\n", damaged)
	}
	if complete > 0 {
		c.out(noiseNormal, "%d file(s) are ok.\n", complete)
	}
	c.out(noiseNormal, "You have %d out of %d data blocks available.\n", have, total)
	if available > 0 {
		c.out(noiseNormal, "You have %d recovery blocks available.\n", available)
	}

	if available < need {
		c.out(noiseQuiet, "Repair is not possible.\n")
		c.out(noiseQuiet, "You need %d more recovery blocks to be able to repair.\n", need-available)
		return exitRepairNotPossible
	}

	c.out(noiseQuiet, "Repair is possible.\n")
	if excess := available - need; excess > 0 {
		c.out(noiseNormal, "You have an excess of %d recovery blocks.\n", excess)
	}
	switch {
	case need > 0:
		c.out(noiseNormal, "%d recovery blocks will be used to repair.\n", need)
	case available > 0:
		c.out(noiseNormal, "None of the recovery blocks will be used for the repair.\n")
	}

	if !repair {
		return exitRepairPossible
	}

	if need > 0 {
		c.out(noiseNormal, "\nComputing Reed Solomon matrix.\nConstructing: done.\nSolving: done.\n\n")
	}

	// Remember what needed rebuilding: only those files are re-verified
	// afterwards, and their sizes are what the byte count reports.
	rebuilt := map[string]bool{}
	var written uint64
	for _, st := range sorted {
		if !st.OK {
			rebuilt[st.File.Name] = true
			written += st.File.Size
		}
	}

	if err := set.Repair(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitRepairFailed
	}
	c.out(noiseNormal, "Writing recovered data\rWrote %d bytes to disk\n", written)

	c.out(noiseQuiet, "\nVerifying repaired files:\n\n")
	after, err := set.Verify(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}
	sort.Slice(after, func(i, j int) bool { return after[i].File.Name < after[j].File.Name })
	for _, st := range after {
		if !rebuilt[st.File.Name] {
			continue
		}
		if !st.OK {
			c.out(noiseQuiet, "Target: \"%s\" - damaged.\n", st.File.Name)
			c.out(noiseQuiet, "\nRepair failed.\n")
			return exitRepairFailed
		}
		c.out(noiseQuiet, "Target: \"%s\" - found.\n", st.File.Name)
	}
	c.out(noiseQuiet, "\nRepair complete.\n")

	if c.purge {
		purge(c, set, dir)
	}
	return exitSuccess
}

// purge removes the recovery files and any leftover backups once the data is
// known to be good, which is what -p asks for.
func purge(c *config, set *par2.Set, dir string) {
	for _, p := range companions(c.archive) {
		if err := os.Remove(p); err == nil {
			c.out(noiseNoisy, "Removed \"%s\".\n", p)
		}
	}
	for _, fd := range set.Files {
		backup := filepath.Join(dir, fd.Name+".1")
		if err := os.Remove(backup); err == nil {
			c.out(noiseNoisy, "Removed \"%s\".\n", backup)
		}
	}
}

func checkPar1(c *config, repair bool) int {
	dir := c.basePath
	if dir == "" {
		dir = filepath.Dir(c.archive)
	}

	base := strings.TrimSuffix(c.archive, filepath.Ext(c.archive))
	vols, _ := filepath.Glob(base + ".p[0-9][0-9]")
	sort.Strings(vols)
	paths := append([]string{c.archive}, vols...)
	for _, p := range paths {
		c.out(noiseQuiet, "Loading \"%s\".\n", p)
	}

	set, err := par1.Parse(paths...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitInsufficientData
	}

	status, err := set.Verify(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFileIOError
	}
	sort.Slice(status, func(i, j int) bool { return status[i].File.Name < status[j].File.Name })

	c.out(noiseNormal, "\nThere are %d recoverable files and 0 other files.\n", len(set.Files))
	c.out(noiseNormal, "\nVerifying source files:\n\n")

	complete, damaged, missing := 0, 0, 0
	for _, st := range status {
		switch {
		case st.OK:
			c.out(noiseQuiet, "Target: \"%s\" - found.\n", st.File.Name)
			complete++
		case !st.Present:
			c.out(noiseQuiet, "Target: \"%s\" - missing.\n", st.File.Name)
			missing++
		default:
			c.out(noiseQuiet, "Target: \"%s\" - damaged.\n", st.File.Name)
			damaged++
		}
	}
	need := damaged + missing
	if need == 0 {
		c.out(noiseQuiet, "\nAll files are correct, repair is not required.\n")
		return exitSuccess
	}

	available := len(set.Recovery)
	c.out(noiseQuiet, "\nRepair is required.\n")
	if missing > 0 {
		c.out(noiseNormal, "%d file(s) are missing.\n", missing)
	}
	if damaged > 0 {
		c.out(noiseNormal, "%d file(s) exist but are damaged.\n", damaged)
	}
	if complete > 0 {
		c.out(noiseNormal, "%d file(s) are ok.\n", complete)
	}
	c.out(noiseNormal, "You have %d recovery volumes available.\n", available)
	if available < need {
		c.out(noiseQuiet, "Repair is not possible.\n")
		c.out(noiseQuiet, "You need %d more recovery volumes to be able to repair.\n", need-available)
		return exitRepairNotPossible
	}
	c.out(noiseQuiet, "Repair is possible.\n")
	if !repair {
		return exitRepairPossible
	}
	if err := set.Repair(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitRepairFailed
	}
	c.out(noiseQuiet, "\nRepair complete.\n")
	return exitSuccess
}
