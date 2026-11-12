package dev

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"testing"
)

// A complete alternate Qualcomm image, with an actual hash table. This is a
// synthetic version variation, not a changed byte in a corrupt signed image.
func alternatePIL(t *testing.T, modem, dir string) {
	t.Helper()
	le := binary.LittleEndian
	const count = 4
	header := make([]byte, 52+32*count)
	copy(header, "\x7fELF\x01\x01\x01")
	le.PutUint16(header[16:], 2)
	le.PutUint16(header[18:], 164)
	le.PutUint32(header[20:], 1)
	le.PutUint32(header[28:], 52)
	le.PutUint16(header[40:], 52)
	le.PutUint16(header[42:], 32)
	le.PutUint16(header[44:], count)
	hashes := make([]byte, 40+sha256.Size*count)
	le.PutUint32(hashes[20:], sha256.Size*count)
	code := []byte("another unit's complete MPSS build\n")
	for i, v := range []struct{ size, flags uint32 }{{uint32(len(header)), 7 << 24}, {uint32(len(hashes)), 2 << 24}, {uint32(len(code)), 0x8000005}, {0, 0x8000006}} {
		p := header[52+i*32:]
		le.PutUint32(p[16:], v.size)
		le.PutUint32(p[24:], v.flags)
	}
	for i, data := range map[int][]byte{0: header, 2: code} {
		h := sha256.Sum256(data)
		copy(hashes[40+i*32:], h[:])
	}
	for name, data := range map[string][]byte{"modem.b00": header, "modem.b01": hashes, "modem.b02": code, "modem.mdt": append(append([]byte(nil), header...), hashes...)} {
		path := filepath.Join(dir, name)
		if e := os.WriteFile(path, data, 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := command("", nil, "mcopy", "-o", "-i", modem, path, "::/image/"+name); e != nil {
			t.Fatal(e)
		}
	}
}

func TestOwnPILAllowsDifferentVersionsAndSegmentLists(t *testing.T) {
	dir := t.TempDir()
	modem := filepath.Join(dir, "modem.img")
	f, e := os.Create(modem)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.Truncate(16 << 20); e != nil {
		t.Fatal(e)
	}
	f.Close()
	for _, args := range [][]string{{"mformat", "-i", modem, "::"}, {"mmd", "-i", modem, "::/image"}} {
		if _, e = command("", nil, args[0], args[1:]...); e != nil {
			t.Fatal(e)
		}
	}
	alternatePIL(t, modem, dir)
	out := filepath.Join(dir, "hardware")
	if e = os.MkdirAll(filepath.Join(out, "firmware/dsp"), 0700); e != nil {
		t.Fatal(e)
	}
	files, e := extractOwnPIL(modem, out, "modem", 164)
	if e != nil || len(files) != 4 {
		t.Fatal("valid alternative firmware rejected", e)
	}
	code, e := os.ReadFile(filepath.Join(out, "firmware/dsp/modem.b02"))
	if e != nil || !bytes.Contains(code, []byte("another unit")) {
		t.Fatal("own code not selected", e)
	}
	bad := filepath.Join(dir, "corrupt")
	mustWrite(t, bad, "different corrupted bytes\n")
	if _, e = command("", nil, "mcopy", "-o", "-i", modem, bad, "::/image/modem.b02"); e != nil {
		t.Fatal(e)
	}
	if _, e = extractOwnPIL(modem, out, "modem", 164); e == nil {
		t.Fatal("mixed/incomplete own firmware accepted")
	}
}

func TestOwnDSPBundleKeepsKernelBinding(t *testing.T) {
	profiles, e := flasher.Catalog()
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range profiles {
		if !p.Qualified || p.FirmwareSource != "own-device" {
			continue
		}
		b := map[string]string{}
		for k, v := range p.Bundle {
			b[k] = v
		}
		for _, key := range []string{"mpss", "wcnss"} {
			b[key] = flasher.HashBytes([]byte("own-unit-" + key))
		}
		if !flasher.CompatibleBundle(p, b) {
			t.Fatal("own firmware version rejected")
		}
		b["kernel"] = flasher.HashBytes([]byte("different kernel"))
		if flasher.CompatibleBundle(p, b) {
			t.Fatal("kernel ABI substitution accepted")
		}
	}
}
