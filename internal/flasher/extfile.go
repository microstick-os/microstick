package flasher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

var ErrExt4FileMissing = errors.New("ext4 file missing")

// ReadExt4File reads only the blocks needed for one small metadata file.
func ReadExt4File(ctx context.Context, d Reader, r Range, helper, path string) ([]byte, error) {
	if helper == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\n\r\x00") {
		return nil, errors.New("ext4 file request")
	}
	cmd := exec.CommandContext(ctx, helper, strconv.FormatUint(r.Length, 10), path)
	in, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	out, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	cmd.Stderr = io.Discard
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
	reader := bufio.NewReader(out)
	var result []byte
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			in.Close()
			if exit, ok := cmd.Wait().(*exec.ExitError); ok && exit.ExitCode() == 4 {
				return nil, ErrExt4FileMissing
			}
			return nil, errors.New("невозможно прочитать метаданные ext4")
		}
		p := strings.Fields(line)
		if len(p) == 1 && p[0] == "DONE" {
			in.Close()
			if e = cmd.Wait(); e != nil {
				return nil, e
			}
			return result, nil
		}
		if len(p) == 2 && p[0] == "F" {
			if result != nil {
				return nil, errors.New("duplicate metadata response")
			}
			n, e := strconv.ParseUint(p[1], 10, 32)
			if e != nil || n > 65536 {
				return nil, errors.New("metadata size")
			}
			result = make([]byte, n)
			if _, e = io.ReadFull(reader, result); e != nil {
				return nil, e
			}
			continue
		}
		if len(p) != 3 || p[0] != "R" {
			return nil, errors.New("metadata broker protocol")
		}
		off, e := strconv.ParseUint(p[1], 10, 64)
		if e != nil {
			return nil, e
		}
		n, e := strconv.ParseUint(p[2], 10, 64)
		if e != nil || n == 0 || n > Chunk || off > r.Length || n > r.Length-off {
			return nil, errors.New("metadata read bounds")
		}
		start := off / 512 * 512
		end := (off + n + 511) / 512 * 512
		if end > r.Length {
			return nil, errors.New("metadata alignment")
		}
		b, e := ReadRange(ctx, d, Range{r.Area, r.Offset + start, end - start}, Chunk+1024)
		if e != nil {
			return nil, e
		}
		if _, e = in.Write(b[off-start : off-start+n]); e != nil {
			return nil, fmt.Errorf("metadata broker: %w", e)
		}
	}
}
