// Command parchive creates, verifies and repairs PAR1 and PAR2 recovery sets.
//
//	parchive create -s 4096 -n 20 archive.par2 file1 file2 ...
//	parchive create -n 5 archive.par file1 file2 ...
//	parchive verify archive.par2
//	parchive repair archive.par2
//
// The format is chosen from the extension of the recovery file: ".par2" for
// PAR2, ".par" for PAR1.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/joamag/parchive-go/par1"
	"github.com/joamag/parchive-go/par2"
)

const creator = "parchive-go"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "create":
		err = create(os.Args[2:])
	case "verify":
		err = check(os.Args[2:], false)
	case "repair":
		err = check(os.Args[2:], true)
	case "-h", "--help", "help":
		usage()
		return
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: parchive create|verify|repair ...

  parchive create [-s size] [-n count] out.par2 files...   create a PAR2 set
  parchive create [-n count] out.par files...              create a PAR1 set
  parchive verify out.par2                                 check every file
  parchive repair out.par2                                 rebuild what is bad

Verify and repair find slices that moved because bytes were inserted or
deleted. Pass -no-scan to check only the offsets slices should be at.

The format follows the extension of the recovery file: .par2 or .par.
`)
}

// isPar1 reports whether the recovery file names a PAR1 set.
func isPar1(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".par")
}

func create(args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	sliceSize := fs.Uint64("s", 4096, "slice size in bytes, a multiple of 4 (PAR2 only)")
	count := fs.Int("n", 10, "number of recovery slices (PAR2) or volumes (PAR1)")
	fs.Parse(args)
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: create [-s size] [-n count] out.par2 files...")
	}
	out, inputs := fs.Arg(0), fs.Args()[1:]
	if isPar1(out) {
		return createPar1(out, inputs, *count)
	}
	return createPar2(out, inputs, *sliceSize, *count)
}

func createPar2(out string, inputs []string, sliceSize uint64, count int) error {
	set, err := par2.Create(inputs, sliceSize, 0, count, creator)
	if err != nil {
		return err
	}
	idx, err := os.Create(out)
	if err != nil {
		return err
	}
	if err := set.WriteIndex(idx); err != nil {
		idx.Close()
		return err
	}
	if err := idx.Close(); err != nil {
		return err
	}

	exps := make([]uint32, count)
	for i := range exps {
		exps[i] = uint32(i)
	}
	vol := fmt.Sprintf("%s.vol%03d+%02d.par2", strings.TrimSuffix(out, ".par2"), 0, count)
	vf, err := os.Create(vol)
	if err != nil {
		return err
	}
	defer vf.Close()
	if err := set.WriteVolume(vf, exps); err != nil {
		return err
	}
	fmt.Printf("wrote %s and %s (%d input slices, %d recovery slices)\n",
		out, vol, set.TotalSlices(), count)
	return nil
}

func createPar1(out string, inputs []string, count int) error {
	set, err := par1.Create(inputs, count, 0)
	if err != nil {
		return err
	}
	idx, err := os.Create(out)
	if err != nil {
		return err
	}
	if err := set.WriteIndex(idx); err != nil {
		idx.Close()
		return err
	}
	if err := idx.Close(); err != nil {
		return err
	}

	base := strings.TrimSuffix(out, filepath.Ext(out))
	for v := 1; v <= count; v++ {
		name := fmt.Sprintf("%s.p%02d", base, v)
		vf, err := os.Create(name)
		if err != nil {
			return err
		}
		if err := set.WriteVolume(vf, uint64(v)); err != nil {
			vf.Close()
			return err
		}
		if err := vf.Close(); err != nil {
			return err
		}
	}
	fmt.Printf("wrote %s and %d volumes (%d files, %d bytes each)\n",
		out, count, len(set.Files), set.VolumeSize)
	return nil
}

func check(args []string, repair bool) error {
	name := "verify"
	if repair {
		name = "repair"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	noScan := fs.Bool("no-scan", false,
		"only look for slices at their own offsets, skipping the search for misplaced data")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: %s [-no-scan] file.par2", name)
	}
	target := fs.Arg(0)
	dir := filepath.Dir(target)
	if isPar1(target) {
		return checkPar1(target, dir, repair)
	}
	return checkPar2(target, dir, repair, par2.Options{NoScan: *noScan})
}

// siblings collects every companion volume next to the given recovery file, so
// that the recovery data is available and not just the index.
func siblings(target, pattern string) []string {
	base := strings.TrimSuffix(target, filepath.Ext(target))
	found, _ := filepath.Glob(base + pattern)
	sort.Strings(found)
	return found
}

func checkPar2(target, dir string, repair bool, o par2.Options) error {
	set, err := par2.Parse(siblings(target, "*.par2")...)
	if err != nil {
		return err
	}
	status, err := set.VerifyWith(dir, o)
	if err != nil {
		return err
	}

	lost, moved, work := 0, 0, false
	for _, st := range status {
		switch {
		case st.OK:
			fmt.Printf("  ok       %s\n", st.File.Name)
			continue
		case !st.Present && len(st.Damaged) == len(st.File.Slices):
			fmt.Printf("  missing  %s (%d slices)\n", st.File.Name, len(st.Damaged))
		case len(st.Damaged) == 0:
			// Every slice was found, just not where it belongs: the file can be
			// rebuilt without spending any recovery data.
			fmt.Printf("  moved    %s (%d/%d slices misplaced)\n",
				st.File.Name, len(st.Misplaced), len(st.File.Slices))
		default:
			fmt.Printf("  damaged  %s (%d/%d slices bad", st.File.Name, len(st.Damaged), len(st.File.Slices))
			if len(st.Misplaced) > 0 {
				fmt.Printf(", %d misplaced", len(st.Misplaced))
			}
			fmt.Println(")")
		}
		lost += len(st.Damaged)
		moved += len(st.Misplaced)
		work = true
	}
	if !work {
		fmt.Println("all files verified")
		return nil
	}

	switch {
	case lost == 0:
		fmt.Printf("%d slices are misplaced, no recovery data needed\n", moved)
	default:
		fmt.Printf("%d slices need repair, %d recovery slices available\n", lost, len(set.Recovery))
	}
	if !repair {
		return nil
	}
	if err := set.RepairWith(dir, o); err != nil {
		return err
	}
	fmt.Println("repaired")
	return nil
}

func checkPar1(target, dir string, repair bool) error {
	paths := append([]string{target}, siblings(target, ".p[0-9][0-9]")...)
	set, err := par1.Parse(paths...)
	if err != nil {
		return err
	}
	status, err := set.Verify(dir)
	if err != nil {
		return err
	}
	bad := 0
	for _, st := range status {
		switch {
		case st.OK:
			fmt.Printf("  ok       %s\n", st.File.Name)
		case !st.Present:
			fmt.Printf("  missing  %s\n", st.File.Name)
			bad++
		default:
			fmt.Printf("  damaged  %s\n", st.File.Name)
			bad++
		}
	}
	if bad == 0 {
		fmt.Println("all files verified")
		return nil
	}
	fmt.Printf("%d files need repair, %d recovery volumes available\n", bad, len(set.Recovery))
	if !repair {
		return nil
	}
	if err := set.Repair(dir); err != nil {
		return err
	}
	fmt.Println("repaired")
	return nil
}
