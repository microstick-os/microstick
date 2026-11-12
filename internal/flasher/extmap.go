package flasher

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// AllocationMap grants a native libext2fs helper read access to exactly one
// partition. Any malformed map/dirty/unsupported filesystem falls back to a
// complete partition copy at the backup layer, without repairing its source.
func AllocationMap(ctx context.Context, d Reader, r Range, helper string) ([]Range, error) {
	return allocationMap(ctx, d, r, helper, false)
}
func allocationMap(ctx context.Context, d Reader, r Range, helper string, candidate bool) ([]Range, error) {
	if helper == "" {
		return nil, errors.New("native libext2fs mapper недоступен")
	}
	args := []string{strconv.FormatUint(r.Length, 10)}
	if candidate {
		args = append(args, "--candidate")
	}
	cmd := exec.CommandContext(ctx, helper, args...)
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	if e = cmd.Start(); e != nil {
		return nil, e
	}
	defer func() {
		in.Close()
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 1024), 4096)
	rs := []Range{}
	done := false
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 1 && parts[0] == "DONE" {
			done = true
			break
		}
		if len(parts) != 3 {
			return nil, errors.New("native mapper protocol")
		}
		off, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			return nil, err
		}
		n, err := strconv.ParseUint(parts[2], 10, 64)
		if err != nil || n == 0 || off > r.Length || n > r.Length-off {
			return nil, errors.New("native mapper bounds")
		}
		switch parts[0] {
		case "R":
			if n > Chunk {
				return nil, errors.New("native mapper read budget")
			}
			start := off / 512 * 512
			end := (off + n + 511) / 512 * 512
			if end > r.Length {
				return nil, errors.New("mapper unaligned range")
			}
			b, err := ReadRange(ctx, d, Range{r.Area, r.Offset + start, end - start}, Chunk+1024)
			if err != nil {
				return nil, err
			}
			if _, err = in.Write(b[off-start : off-start+n]); err != nil {
				return nil, err
			}
		case "M":
			if off%512 != 0 || n%512 != 0 || len(rs) > 1<<20 {
				return nil, errors.New("mapper map geometry")
			}
			rs = append(rs, Range{r.Area, r.Offset + off, n})
		default:
			return nil, errors.New("native mapper operation forbidden")
		}
	}
	in.Close()
	if !done {
		_ = cmd.Wait()
		return nil, fmt.Errorf("ненадёжная карта ext4: %s", strings.TrimSpace(diagnostic.String()))
	}
	if e = scanner.Err(); e != nil {
		return nil, e
	}
	if e = cmd.Wait(); e != nil {
		return nil, errors.New("libext2fs отказался от компактного копирования")
	}
	info, e := d.Info(ctx)
	if e != nil {
		return nil, e
	}
	if len(rs) == 0 {
		return nil, errors.New("empty allocation map")
	}
	if e = validateRanges(rs, info.Geometry); e != nil {
		return nil, e
	}
	return rs, nil
}
