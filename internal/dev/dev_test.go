package dev

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPaths(t *testing.T) Paths {
	t.Helper()
	repo, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	p, e := paths(repo, true)
	if e != nil {
		t.Skip("set MICROSTICK_WORKSPACE for integration tests:", e)
	}
	return p
}
func mustWrite(t *testing.T, path, text string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestIMEIVectors(t *testing.T) {
	for _, v := range []struct {
		tac    string
		serial uint32
		want   string
	}{{"49015420", 323751, "490154203237518"}, {"35290611", 7, "352906110000074"}} {
		got, e := FormatIMEI(v.tac, v.serial)
		if e != nil || got != v.want || !ValidIMEI(got) {
			t.Fatalf("%s: %s %v", v.tac, got, e)
		}
	}
	for _, v := range []string{"", "1234567", "123456789", "00000000", "１２３４５６７８", "1234567;"} {
		if _, e := GenerateIMEI(v); e == nil {
			t.Fatal("accepted invalid TAC", v)
		}
	}
	if ValidIMEI("490154203237519") || ValidIMEI("000000000000000") {
		t.Fatal("bad IMEI accepted")
	}
}
func TestPBKDF2KnownVectors(t *testing.T) {
	got := hex.EncodeToString(pbkdf2([]byte("password"), []byte("salt"), 1, 20, sha1.New))
	if got != "0c60c80f961f0e71f3a9b524af6012062fe037a6" {
		t.Fatal(got)
	}
	got = hex.EncodeToString(pbkdf2([]byte("password"), []byte("salt"), 1, 32, sha256.New))
	if got != "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b" {
		t.Fatal(got)
	}
}
func TestManifestTraversalAndCorruption(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "x"), "data")
	v, e := entry(root, "x")
	if e != nil {
		t.Fatal(e)
	}
	if e = verifyEntries(root, []Entry{v}); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, filepath.Join(root, "x"), "corrupt")
	if verifyEntries(root, []Entry{v}) == nil {
		t.Fatal("corruption accepted")
	}
	if _, e = safeJoin(root, "../private"); e == nil {
		t.Fatal("path escape accepted")
	}
	if e = os.Symlink("/etc/passwd", filepath.Join(root, "escape")); e != nil {
		t.Fatal(e)
	}
	if _, e = safeJoin(root, "escape"); e == nil {
		t.Fatal("symlink escape accepted")
	}
}
func TestPublicAllowlistRejectsArtifactsAndPython(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "public\n")
	if _, e := PublicFiles(root); e != nil {
		t.Fatal(e)
	}
	mustWrite(t, filepath.Join(root, "scripts/check.py"), "print('forbidden')\n")
	if _, e := PublicFiles(root); e == nil {
		t.Fatal("Python source accepted")
	}
	os.Remove(filepath.Join(root, "scripts/check.py"))
	mustWrite(t, filepath.Join(root, "firmware.bin"), "blob")
	if _, e := PublicFiles(root); e == nil {
		t.Fatal("artifact accepted")
	}
}
func TestPublicDocumentationImages(t *testing.T) {
	var data bytes.Buffer
	if e := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 1, 1))); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		name, path, data string
		allowed          bool
	}{
		{"english", "docs/images/desktop_en.png", data.String(), true},
		{"russian", "docs/images/desktop_ru.png", data.String(), true},
		{"invalid", "docs/images/desktop_en.png", "not a PNG", false},
		{"private-key", "docs/images/desktop_en.png", data.String() + "\n-----BEGIN PRIVATE KEY-----\n", false},
		{"other-image", "docs/images/private.png", data.String(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, tc.path), tc.data)
			names, e := PublicFiles(root)
			if (e == nil) != tc.allowed {
				t.Fatalf("allowed=%v: %v", tc.allowed, e)
			}
			if tc.allowed && (len(names) != 1 || names[0] != tc.path) {
				t.Fatal("documentation image omitted", names)
			}
		})
	}
}
func TestCPIOBootRoundtrip(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "init"), "#!/bin/sh\n")
	if e := os.Symlink("init", filepath.Join(root, "link")); e != nil {
		t.Fatal(e)
	}
	data, e := NewCPIO(root, epoch)
	if e != nil {
		t.Fatal(e)
	}
	gz, e := gzip.NewReader(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	var raw bytes.Buffer
	if _, e = raw.ReadFrom(gz); e != nil {
		t.Fatal(e)
	}
	names, e := cpioNames(raw.Bytes())
	if e != nil {
		t.Fatal(e)
	}
	if strings.Join(names, ",") != ".,init,link,TRAILER!!!" {
		t.Fatal(names)
	}
	layout := BootLayout{"0x80080000", "0x82000000", "0x81e00000", 2048, "console=test", ""}
	boot, e := MakeBoot([]byte("kernel"), data, layout)
	if e != nil {
		t.Fatal(e)
	}
	if binary.LittleEndian.Uint32(boot[8:]) != 6 || string(boot[2048:2054]) != "kernel" {
		t.Fatal("boot data mismatch")
	}
	if _, e = cpioNames(raw.Bytes()[:len(raw.Bytes())/2]); e == nil {
		t.Fatal("truncated cpio accepted")
	}
}
func TestSparseConversionAndBounds(t *testing.T) {
	root := t.TempDir()
	var sparse bytes.Buffer
	header := make([]byte, 28)
	binary.LittleEndian.PutUint32(header, 0xed26ff3a)
	binary.LittleEndian.PutUint16(header[4:], 1)
	binary.LittleEndian.PutUint16(header[8:], 28)
	binary.LittleEndian.PutUint16(header[10:], 12)
	binary.LittleEndian.PutUint32(header[12:], 4)
	binary.LittleEndian.PutUint32(header[16:], 3)
	binary.LittleEndian.PutUint32(header[20:], 3)
	sparse.Write(header)
	for i, kind := range []uint16{0xcac1, 0xcac2, 0xcac3} {
		h := make([]byte, 12)
		binary.LittleEndian.PutUint16(h, kind)
		binary.LittleEndian.PutUint32(h[4:], 1)
		size := uint32(16)
		if i == 2 {
			size = 12
		}
		binary.LittleEndian.PutUint32(h[8:], size)
		sparse.Write(h)
		if i < 2 {
			sparse.Write([]byte("data"))
		}
	}
	source, dest := filepath.Join(root, "sparse"), filepath.Join(root, "raw")
	os.WriteFile(source, sparse.Bytes(), 0600)
	if e := SparseToRaw(source, dest); e != nil {
		t.Fatal(e)
	}
	got, e := os.ReadFile(dest)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(got, append([]byte("datadata"), 0, 0, 0, 0)) {
		t.Fatal(got)
	}
	binary.LittleEndian.PutUint32(header[16:], 1)
	copy(sparse.Bytes(), header)
	os.WriteFile(source, sparse.Bytes(), 0600)
	if SparseToRaw(source, filepath.Join(root, "bad")) == nil {
		t.Fatal("sparse bounds ignored")
	}
}
func TestGPTCRCAndOverlap(t *testing.T) {
	data := make([]byte, 34*512)
	copy(data[510:], []byte{0x55, 0xaa})
	h := data[512:1024]
	copy(h, "EFI PART")
	binary.LittleEndian.PutUint32(h[8:], 0x10000)
	binary.LittleEndian.PutUint32(h[12:], 92)
	binary.LittleEndian.PutUint64(h[24:], 1)
	binary.LittleEndian.PutUint64(h[32:], 1000)
	binary.LittleEndian.PutUint64(h[40:], 34)
	binary.LittleEndian.PutUint64(h[48:], 967)
	binary.LittleEndian.PutUint64(h[72:], 2)
	binary.LittleEndian.PutUint32(h[80:], 1)
	binary.LittleEndian.PutUint32(h[84:], 128)
	a := data[1024:1152]
	a[0] = 1
	binary.LittleEndian.PutUint64(a[32:], 34)
	binary.LittleEndian.PutUint64(a[40:], 99)
	for i, c := range "modem" {
		binary.LittleEndian.PutUint16(a[56+i*2:], uint16(c))
	}
	binary.LittleEndian.PutUint32(h[88:], crc32.ChecksumIEEE(a))
	binary.LittleEndian.PutUint32(h[16:], crc32.ChecksumIEEE(h[:92]))
	g, e := ParseGPT(data, 1)
	if e != nil || len(g.Partitions) != 1 || g.Partitions[0].Sectors != 66 {
		t.Fatalf("%+v %v", g, e)
	}
	data[1024] ^= 1
	if _, e = ParseGPT(data, 1); e == nil {
		t.Fatal("GPT corruption accepted")
	}
}
func TestRealExt4PersonalizationRetryAndNewInstall(t *testing.T) {
	p := testPaths(t)
	p.Work = t.TempDir()
	root := t.TempDir()
	base := filepath.Join(root, "base")
	tree := filepath.Join(root, "tree")
	os.MkdirAll(base, 0700)
	os.MkdirAll(filepath.Join(tree, "usr/share/microstick"), 0755)
	mustWrite(t, filepath.Join(base, "boot.img"), "synthetic-boot")
	if _, e := command("", nil, "mkfs.ext4", "-q", "-F", "-d", tree, filepath.Join(base, "rootfs.ext4"), "16384"); e != nil {
		t.Fatal(e)
	}
	if e := finalize(base, Manifest{Version: 2}); e != nil {
		t.Fatal(e)
	}
	before, _ := hashFile(filepath.Join(base, "rootfs.ext4"))
	state, output := filepath.Join(root, "first.json"), filepath.Join(root, "first")
	if e := Prepare(p, base, state, output, ""); e != nil {
		t.Fatal(e)
	}
	var record Installation
	if e := readJSON(state, &record); e != nil {
		t.Fatal(e)
	}
	if !ValidIMEI(record.IMEI) {
		t.Fatal("generated invalid IMEI")
	}
	got, e := imageDefault(filepath.Join(output, "rootfs.ext4"))
	if e != nil || got != record.IMEI {
		t.Fatalf("default mismatch: %s %v", got, e)
	}
	if e = Prepare(p, base, state, output, ""); e != nil {
		t.Fatal(e)
	}
	after, _ := hashFile(filepath.Join(base, "rootfs.ext4"))
	if after != before {
		t.Fatal("base changed")
	}
	if e = Prepare(p, base, state, filepath.Join(root, "different"), "49015420"); e == nil {
		t.Fatal("mismatching retry TAC accepted")
	}
	if e = Prepare(p, base, filepath.Join(root, "second.json"), filepath.Join(root, "second"), ""); e != nil {
		t.Fatal(e)
	}
	var other Installation
	readJSON(filepath.Join(root, "second.json"), &other)
	if record.ID == other.ID {
		t.Fatal("installation identity reused")
	}
	data, e := os.ReadFile(state)
	if e != nil {
		t.Fatal(e)
	}
	if !json.Valid(data) {
		t.Fatal("invalid persisted state")
	}
	st, _ := os.Stat(state)
	if st.Mode().Perm() != 0600 {
		t.Fatal("private record mode")
	}
}
func TestCachedOfficialDownloadsAndExtractedHardware(t *testing.T) {
	p := testPaths(t)
	for _, v := range []struct{ name, hash string }{{"runtime-filesystem.zip", "8be6f100e8fc3f612e0b03c199d01f51d38b953a19343ad0f4cf7a2f7a57f603"}, {"runtime-boot.img", "a2b527e98a5cee0a4c3cad5099af7ce0648246f19d20d13ef2c168e400c81b33"}, {"modem-loader.zip", "bb300d6f29a43982d14633254c7de3c810490c5b7f6a206a20c47922ac1802dc"}} {
		path := filepath.Join(p.Work, "buildroot-downloads", v.name)
		if _, e := os.Stat(path); os.IsNotExist(e) {
			t.Skip("optional upstream archive not cached")
		}
		h, e := hashFile(path)
		if e != nil || h != v.hash {
			t.Fatal("pinned input mismatch", v.name, e)
		}
	}
	var m Manifest
	if e := readJSON(filepath.Join(p.Hardware, "MANIFEST.json"), &m); e != nil {
		t.Fatal(e)
	}
	if e := verifyEntries(p.Hardware, m.Artifacts); e != nil {
		t.Fatal(e)
	}
}
