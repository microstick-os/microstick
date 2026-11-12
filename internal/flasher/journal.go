package flasher

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"
)

func appendJournal(f *os.File, v InstallJournal) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	if _, e = f.Write(append(b, '\n')); e != nil {
		return e
	}
	return f.Sync()
}
func hashOpenFile(f *os.File) (string, error) {
	if _, e := f.Seek(0, io.SeekStart); e != nil {
		return "", e
	}
	h := sha256.New()
	_, e := io.Copy(h, f)
	return hex.EncodeToString(h.Sum(nil)), e
}

// Only fsynced readback receipts are resumable. A trailing torn journal entry
// represents an incomplete transition and cannot authorize a skip.
func completedPayload(path, plan string) (map[int]bool, error) {
	if e := retainTornJournal(path); e != nil {
		return nil, e
	}
	result := map[int]bool{}
	f, e := os.Open(path)
	if os.IsNotExist(e) {
		return result, nil
	}
	if e != nil {
		return nil, e
	}
	defer f.Close()
	reader := bufio.NewReader(f)
	for {
		line, e := reader.ReadBytes('\n')
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		var entry InstallJournal
		if json.Unmarshal(line, &entry) != nil || entry.Version != Schema {
			return nil, errors.New("invalid saved journal")
		}
		if entry.PlanSHA256 != plan {
			return nil, errors.New("saved journal belongs to a different plan")
		}
		if entry.Operation >= 0 {
			if entry.Phase == "write-begin" {
				delete(result, entry.Operation)
			}
			if entry.Phase == "readback-complete" {
				result[entry.Operation] = true
			}
		}
	}
	return result, nil
}

// Preserve a power-loss tail byte-for-byte, then continue from its complete
// records in a fresh current journal. Never append JSON onto a torn JSON line.
func retainTornJournal(path string) error {
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if len(b) == 0 || b[len(b)-1] == '\n' {
		return nil
	}
	end := bytes.LastIndexByte(b, '\n') + 1
	saved := path + ".torn-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if e = os.Rename(path, saved); e != nil {
		return e
	}
	return atomicFile(path, b[:end])
}
