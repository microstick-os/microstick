package dev

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func fixtureGPT() []byte {
	data := make([]byte, 1001*512)
	copy(data[510:], []byte{0x55, 0xaa})
	h := data[512:1024]
	copy(h, "EFI PART")
	binary.LittleEndian.PutUint32(h[8:], 0x10000)
	binary.LittleEndian.PutUint32(h[12:], 92)
	binary.LittleEndian.PutUint64(h[24:], 1)
	binary.LittleEndian.PutUint64(h[32:], 1000)
	binary.LittleEndian.PutUint64(h[40:], 34)
	binary.LittleEndian.PutUint64(h[48:], 967)
	h[56] = 1
	binary.LittleEndian.PutUint64(h[72:], 2)
	binary.LittleEndian.PutUint32(h[80:], 128)
	binary.LittleEndian.PutUint32(h[84:], 128)
	a := data[1024 : 1024+128*128]
	a[0] = 1
	binary.LittleEndian.PutUint64(a[32:], 34)
	binary.LittleEndian.PutUint64(a[40:], 99)
	for i, c := range "modem" {
		binary.LittleEndian.PutUint16(a[56+i*2:], uint16(c))
	}
	binary.LittleEndian.PutUint32(h[88:], crc32.ChecksumIEEE(a))
	sealHeader(h)
	tail := data[1000*512:]
	copy(tail, h)
	binary.LittleEndian.PutUint64(tail[24:], 1000)
	binary.LittleEndian.PutUint64(tail[32:], 1)
	binary.LittleEndian.PutUint64(tail[72:], 968)
	sealHeader(tail)
	copy(data[968*512:], a)
	return data
}
func sealHeader(h []byte) {
	binary.LittleEndian.PutUint32(h[16:], 0)
	binary.LittleEndian.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))
}
func TestGPTMirrorCorruptionAndBounds(t *testing.T) {
	for _, v := range []struct {
		name   string
		change func([]byte)
	}{
		{"valid", func([]byte) {}}, {"header_crc", func(d []byte) { d[1000*512+20] ^= 1 }}, {"array_corruption", func(d []byte) { d[968*512] ^= 1 }}, {"wrong_device", func(d []byte) { d[1000*512+56] ^= 1; sealHeader(d[1000*512:]) }}, {"wrong_location", func(d []byte) { binary.LittleEndian.PutUint64(d[1000*512+24:], 999); sealHeader(d[1000*512:]) }}, {"array_outside_tail", func(d []byte) { binary.LittleEndian.PutUint64(d[1000*512+72:], 900); sealHeader(d[1000*512:]) }},
	} {
		t.Run(v.name, func(t *testing.T) {
			d := fixtureGPT()
			v.change(d)
			root := t.TempDir()
			disk := filepath.Join(root, "disk")
			if e := os.WriteFile(disk, d, 0600); e != nil {
				t.Fatal(e)
			}
			g, e := ParseGPT(d[:34*512], 1)
			if e != nil {
				t.Fatal(e)
			}
			e = verifyGPTMirror(diskReader{disk}, d, g, root)
			if (e == nil) != (v.name == "valid") {
				t.Fatalf("mirror validation: %v", e)
			}
		})
	}
	root := t.TempDir()
	disk := filepath.Join(root, "disk")
	os.WriteFile(disk, make([]byte, 512), 0600)
	if e := (diskReader{disk}).Read(^uint64(0), 1, filepath.Join(root, "bad")); e == nil {
		t.Fatal("overflow read accepted")
	}
}
func TestQDLReadOnlyInvocation(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "qdl")
	log := filepath.Join(root, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + log + "'\ncase \"$*\" in *' read 0/34+2 '*) ;; *) exit 33;; esac\ndd if=/dev/zero of=\"$8\" bs=512 count=2 status=none\n"
	if e := os.WriteFile(tool, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	dest := filepath.Join(root, "output")
	r := qdlReader{tool, "programmer", "serial123"}
	if e := r.Read(34, 2, dest); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(log)
	if e != nil {
		t.Fatal(e)
	}
	want := "--backend=usb\n--storage=emmc\n--skip-reset\n--serial=serial123\nprogrammer\nread\n0/34+2\n" + dest + "\n"
	if string(b) != want {
		t.Fatalf("QDL invocation differs: %q", b)
	}
}

type rejectReader struct{ calls [][2]uint64 }

func (r *rejectReader) Read(start, count uint64, dest string) error {
	r.calls = append(r.calls, [2]uint64{start, count})
	return os.WriteFile(dest, make([]byte, count*512), 0600)
}
func TestExtractionFailureIsAtomicAndExistingOutputUntouched(t *testing.T) {
	root := t.TempDir()
	p := Paths{Repo: filepath.Join(root, "repo")}
	os.Mkdir(p.Repo, 0755)
	out := filepath.Join(root, "bundle")
	r := &rejectReader{}
	if e := Extract(p, r, out); e == nil {
		t.Fatal("invalid GPT accepted")
	}
	if _, e := os.Lstat(out); !os.IsNotExist(e) {
		t.Fatal("partial bundle published")
	}
	if !reflect.DeepEqual(r.calls, [][2]uint64{{0, 34}}) {
		t.Fatal("read payload after invalid GPT")
	}
	mustWrite(t, filepath.Join(out, "keep"), "unchanged")
	r.calls = nil
	if e := Extract(p, r, out); e == nil {
		t.Fatal("existing output overwritten")
	}
	if len(r.calls) != 0 {
		t.Fatal("device read before existing-output check")
	}
}
