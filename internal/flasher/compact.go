package flasher

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// A dirty ext4 bitmap is provisional. Replay its journal only in a disposable
// sparse copy, then include the union of original and replayed allocations.
// The saved bytes and on-device journal remain unchanged. Verification repeats
// the replay and proves that every live block is present before accepting it.
func journalCompact(ctx context.Context, d Reader, r Range, mapper, dir string) ([]Range, string, error) {
	initial, e := allocationMap(ctx, d, r, mapper, true)
	if e != nil {
		return nil, "", e
	}
	f, e := os.CreateTemp(dir, ".compact-original-*.img")
	if e != nil {
		return nil, "", e
	}
	path := f.Name()
	defer f.Close()
	if e = f.Truncate(int64(r.Length)); e != nil {
		return nil, "", e
	}
	if e = copyRanges(ctx, d, r, initial, f); e != nil {
		return nil, "", e
	}
	needed, e := replayMap(ctx, path, r, initial, mapper, dir)
	if e != nil {
		return nil, "", e
	}
	missing := subtractRanges(needed, initial)
	if e = copyRanges(ctx, d, r, missing, f); e != nil {
		return nil, "", e
	}
	return mergeRanges(append(initial, needed...)), path, nil
}

func copyRanges(ctx context.Context, d Reader, r Range, ranges []Range, f *os.File) error {
	for _, x := range ranges {
		if e := ctx.Err(); e != nil {
			return e
		}
		if _, e := f.Seek(int64(x.Offset-r.Offset), io.SeekStart); e != nil {
			return e
		}
		if e := d.Read(ctx, x, f); e != nil {
			return e
		}
	}
	return nil
}

func replayMap(ctx context.Context, source string, r Range, saved []Range, mapper, dir string) ([]Range, error) {
	f, e := os.CreateTemp(dir, ".compact-replay-*.img")
	if e != nil {
		return nil, e
	}
	path := f.Name()
	defer os.Remove(path)
	if e = f.Truncate(int64(r.Length)); e != nil {
		f.Close()
		return nil, e
	}
	original, e := OpenFiles(map[string]string{"user": source}, "journal-copy", false)
	if e != nil {
		f.Close()
		return nil, e
	}
	local := make([]Range, len(saved))
	for i, x := range saved {
		local[i] = Range{"user", x.Offset - r.Offset, x.Length}
	}
	e = copyRanges(ctx, original, Range{"user", 0, r.Length}, local, f)
	original.Close()
	f.Close()
	if e != nil {
		return nil, e
	}
	// Preen also fixes stale summary counters/bitmap padding left by stock
	// Android. Only this disposable copy is repaired; uncorrectable errors fail.
	cmd := exec.CommandContext(ctx, "e2fsck", "-p", path)
	if _, e = cmd.Output(); e != nil {
		var status *exec.ExitError
		if !errors.As(e, &status) || status.ExitCode() != 1 {
			return nil, errors.New("не удалось проверить журнал ext4 на временной копии")
		}
	}
	clean, e := OpenFiles(map[string]string{"user": path}, "journal-verified", false)
	if e != nil {
		return nil, e
	}
	defer clean.Close()
	used, e := AllocationMap(ctx, clean, Range{"user", 0, r.Length}, mapper)
	if e != nil {
		return nil, e
	}
	for i := range used {
		used[i].Area = r.Area
		used[i].Offset += r.Offset
	}
	return used, nil
}

func mergeRanges(rs []Range) []Range {
	rs = sortedRanges(rs)
	var out []Range
	for _, x := range rs {
		if len(out) > 0 {
			last := &out[len(out)-1]
			if last.Area == x.Area && x.Offset <= last.Offset+last.Length {
				last.Length = max(last.Offset+last.Length, x.Offset+x.Length) - last.Offset
				continue
			}
		}
		out = append(out, x)
	}
	return out
}
func subtractRanges(rs, covered []Range) []Range {
	var out []Range
	for _, r := range mergeRanges(rs) {
		pos, end := r.Offset, r.Offset+r.Length
		for _, c := range mergeRanges(covered) {
			if c.Area != r.Area || c.Offset+c.Length <= pos || c.Offset >= end {
				continue
			}
			if c.Offset > pos {
				out = append(out, Range{r.Area, pos, c.Offset - pos})
			}
			pos = max(pos, min(end, c.Offset+c.Length))
		}
		if pos < end {
			out = append(out, Range{r.Area, pos, end - pos})
		}
	}
	return out
}

func verifyJournalRegion(ctx context.Context, d Reader, region BackupRegion, mapper, dir string) error {
	f, e := os.CreateTemp(dir, ".compact-verify-*.img")
	if e != nil {
		return e
	}
	path := f.Name()
	defer os.Remove(path)
	if e = f.Truncate(int64(region.Range.Length)); e == nil {
		e = copyRanges(ctx, d, region.Range, region.Allocated, f)
	}
	f.Close()
	if e != nil {
		return e
	}
	needed, e := replayMap(ctx, path, region.Range, region.Allocated, mapper, filepath.Dir(path))
	if e != nil {
		return e
	}
	if len(subtractRanges(needed, region.Allocated)) != 0 {
		return errors.New("в бэкапе пропущены занятые блоки после восстановления журнала")
	}
	return nil
}
