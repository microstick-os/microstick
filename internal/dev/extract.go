package dev

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"microstick.dev/tools/internal/flasher"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
)

const qdlRevision = "c72e7d152acd605de19b64ad4a605ae5b58a7cda"
const programmerSHA = "53f193500c03248f0d671ab57bfe9ca8a42967e97f28403294b4b3f854075aca"

type Partition struct {
	Name    string `json:"name"`
	Start   uint64 `json:"start_sector"`
	Sectors uint64 `json:"sectors"`
}
type GPT struct {
	BackupLBA  uint64      `json:"backup_lba"`
	EntriesLBA uint64      `json:"entries_lba"`
	Partitions []Partition `json:"partitions"`
	Entries    []byte      `json:"-"`
}

func ParseGPT(data []byte, lba uint64) (GPT, error) {
	var g GPT
	if len(data) < 1024 || string(data[512:520]) != "EFI PART" || !bytes.Equal(data[510:512], []byte{0x55, 0xaa}) {
		return g, errors.New("invalid primary GPT/MBR")
	}
	h := data[512:1024]
	size := binary.LittleEndian.Uint32(h[12:])
	if size < 92 || size > 512 {
		return g, errors.New("invalid GPT header size")
	}
	copyHeader := append([]byte(nil), h[:size]...)
	expected := binary.LittleEndian.Uint32(h[16:])
	for i := 16; i < 20; i++ {
		copyHeader[i] = 0
	}
	if crc32.ChecksumIEEE(copyHeader) != expected {
		return g, errors.New("GPT header CRC mismatch")
	}
	if binary.LittleEndian.Uint64(h[24:]) != lba {
		return g, errors.New("GPT current LBA mismatch")
	}
	if binary.LittleEndian.Uint32(h[8:]) != 0x10000 || lba != 1 {
		return g, errors.New("unsupported GPT revision/location")
	}
	g.BackupLBA = binary.LittleEndian.Uint64(h[32:])
	g.EntriesLBA = binary.LittleEndian.Uint64(h[72:])
	count := binary.LittleEndian.Uint32(h[80:])
	entrySize := binary.LittleEndian.Uint32(h[84:])
	if count == 0 || count > 128 || entrySize < 128 || entrySize > 512 || entrySize%128 != 0 || g.EntriesLBA < 2 || g.EntriesLBA > 32 || g.BackupLBA < 67 || g.BackupLBA > 1<<32 {
		return g, errors.New("GPT entry geometry outside supported bounds")
	}
	start := g.EntriesLBA * 512
	end := start + uint64(count)*uint64(entrySize)
	if end > uint64(len(data)) {
		return g, errors.New("truncated GPT entries")
	}
	g.Entries = append([]byte(nil), data[start:end]...)
	if crc32.ChecksumIEEE(g.Entries) != binary.LittleEndian.Uint32(h[88:]) {
		return g, errors.New("GPT array CRC mismatch")
	}
	firstUsable, lastUsable := binary.LittleEndian.Uint64(h[40:]), binary.LittleEndian.Uint64(h[48:])
	arraySectors := (uint64(len(g.Entries)) + 511) / 512
	if firstUsable < g.EntriesLBA+arraySectors || firstUsable > lastUsable || lastUsable >= g.BackupLBA-arraySectors {
		return g, errors.New("GPT usable range overlaps metadata")
	}
	seen := map[string]bool{}
	for i := uint32(0); i < count; i++ {
		entry := g.Entries[i*entrySize : (i+1)*entrySize]
		if bytes.Equal(entry[:16], make([]byte, 16)) {
			continue
		}
		var chars []uint16
		for k := 56; k < 128; k += 2 {
			v := binary.LittleEndian.Uint16(entry[k:])
			if v == 0 {
				break
			}
			chars = append(chars, v)
		}
		name := string(utf16.Decode(chars))
		first := binary.LittleEndian.Uint64(entry[32:])
		last := binary.LittleEndian.Uint64(entry[40:])
		if !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(name) || seen[name] || first > last || last > lastUsable || first < firstUsable {
			return g, errors.New("invalid/duplicate GPT partition")
		}
		seen[name] = true
		g.Partitions = append(g.Partitions, Partition{name, first, last - first + 1})
	}
	sort.Slice(g.Partitions, func(i, j int) bool { return g.Partitions[i].Start < g.Partitions[j].Start })
	for i := 1; i < len(g.Partitions); i++ {
		a, b := g.Partitions[i-1], g.Partitions[i]
		if a.Start+a.Sectors > b.Start {
			return g, errors.New("GPT partitions overlap")
		}
	}
	return g, nil
}

// Read and validate the mirror before any partition payload. Both GPT copies
// must describe the same device and partitions; no repair or storage write occurs.
func verifyGPTMirror(reader rangeReader, primary []byte, g GPT, temp string) error {
	const sectors = 33
	path := filepath.Join(temp, "gpt-backup.bin")
	if e := reader.Read(g.BackupLBA-sectors+1, sectors, path); e != nil {
		return e
	}
	tail, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	if len(tail) != sectors*512 {
		return errors.New("short backup GPT")
	}
	h := tail[len(tail)-512:]
	p := primary[512:1024]
	size := binary.LittleEndian.Uint32(h[12:])
	if string(h[:8]) != "EFI PART" || size < 92 || size > 512 {
		return errors.New("invalid backup GPT header")
	}
	header := append([]byte(nil), h[:size]...)
	for i := 16; i < 20; i++ {
		header[i] = 0
	}
	if crc32.ChecksumIEEE(header) != binary.LittleEndian.Uint32(h[16:]) {
		return errors.New("backup GPT header CRC mismatch")
	}
	if !bytes.Equal(h[8:16], p[8:16]) || binary.LittleEndian.Uint64(h[24:]) != g.BackupLBA || binary.LittleEndian.Uint64(h[32:]) != 1 || !bytes.Equal(h[40:72], p[40:72]) || !bytes.Equal(h[80:92], p[80:92]) {
		return errors.New("primary/backup GPT metadata mismatch")
	}
	arrayLBA := binary.LittleEndian.Uint64(h[72:])
	firstLBA := g.BackupLBA - sectors + 1
	if arrayLBA < firstLBA || arrayLBA >= g.BackupLBA {
		return errors.New("backup GPT array outside tail")
	}
	start := (arrayLBA - firstLBA) * 512
	end := start + uint64(len(g.Entries))
	if end > uint64(len(tail)-512) || !bytes.Equal(tail[start:end], g.Entries) {
		return errors.New("primary/backup GPT arrays mismatch")
	}
	return nil
}
func SparseToRaw(source, dest string) error {
	f, e := os.Open(source)
	if e != nil {
		return e
	}
	defer f.Close()
	header := make([]byte, 28)
	if _, e = io.ReadFull(f, header); e != nil {
		return e
	}
	if binary.LittleEndian.Uint32(header) != 0xed26ff3a {
		return copyFile(source, dest, 0600)
	}
	if binary.LittleEndian.Uint16(header[4:]) != 1 {
		return errors.New("unsupported Android sparse version")
	}
	fh := binary.LittleEndian.Uint16(header[8:])
	ch := binary.LittleEndian.Uint16(header[10:])
	block := binary.LittleEndian.Uint32(header[12:])
	total := binary.LittleEndian.Uint32(header[16:])
	chunks := binary.LittleEndian.Uint32(header[20:])
	if fh < 28 || ch < 12 || block == 0 || block > 1024*1024 || uint64(total)*uint64(block) > 8*1024*1024*1024 {
		return errors.New("invalid sparse geometry")
	}
	if _, e = f.Seek(int64(fh), io.SeekStart); e != nil {
		return e
	}
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer out.Close()
	var written int64
	for i := uint32(0); i < chunks; i++ {
		raw := make([]byte, ch)
		if _, e = io.ReadFull(f, raw); e != nil {
			return e
		}
		kind := binary.LittleEndian.Uint16(raw)
		blocks := binary.LittleEndian.Uint32(raw[4:])
		size := binary.LittleEndian.Uint32(raw[8:])
		n := int64(blocks) * int64(block)
		if n < 0 || written+n > int64(total)*int64(block) {
			return errors.New("sparse chunk exceeds output bounds")
		}
		payload := int64(size) - int64(ch)
		switch kind {
		case 0xcac1:
			if payload != n {
				return errors.New("sparse raw chunk length mismatch")
			}
			if _, e = io.CopyN(out, f, n); e != nil {
				return e
			}
		case 0xcac2:
			if payload != 4 {
				return errors.New("sparse fill length mismatch")
			}
			fill := make([]byte, 4)
			if _, e = io.ReadFull(f, fill); e != nil {
				return e
			}
			buffer := bytes.Repeat(fill, 16384)
			for remaining := n; remaining > 0; {
				take := int64(len(buffer))
				if remaining < take {
					take = remaining
				}
				if _, e = out.Write(buffer[:take]); e != nil {
					return e
				}
				remaining -= take
			}
		case 0xcac3:
			if payload != 0 {
				return errors.New("sparse hole payload mismatch")
			}
			if _, e = out.Seek(n, io.SeekCurrent); e != nil {
				return e
			}
		case 0xcac4:
			if payload != 4 || blocks != 0 {
				return errors.New("sparse CRC chunk mismatch")
			}
			if _, e = io.CopyN(io.Discard, f, 4); e != nil {
				return e
			}
		default:
			return errors.New("unsupported sparse chunk type")
		}
		written += n
	}
	if written != int64(total)*int64(block) {
		return errors.New("sparse block count mismatch")
	}
	return out.Truncate(written)
}
func ensureQDL(p Paths) (string, error) {
	out := filepath.Join(p.Work, "host-tools/bin/qdl-microstick-read")
	if _, e := os.Stat(out); e == nil {
		return out, nil
	}
	archive := filepath.Join(p.Work, "buildroot-downloads/qdl-"+qdlRevision+".tar.gz")
	if e := Fetch(archive, "https://codeload.github.com/linux-msm/qdl/tar.gz/"+qdlRevision, "e0110b6f667cc937baa0a333e8fb0187d010db7dd78ba68092223d8c4397c21d"); e != nil {
		return "", e
	}
	sourceRoot := filepath.Join(p.Work, "host-tools/src")
	if e := os.MkdirAll(sourceRoot, 0755); e != nil {
		return "", e
	}
	if _, e := command("", nil, "tar", "-xf", archive, "-C", sourceRoot); e != nil {
		return "", e
	}
	source := filepath.Join(sourceRoot, "qdl-"+qdlRevision)
	// Limit USB transfers to the MSM8916 programmer's supported 16KiB payload.
	usb := filepath.Join(source, "src/usb.c")
	data, e := os.ReadFile(usb)
	if e != nil {
		return "", e
	}
	old := []byte("qdl->max_payload_size = 1048576;")
	if bytes.Count(data, old) != 1 {
		return "", errors.New("pinned QDL transfer-limit patch no longer applies")
	}
	if e = os.WriteFile(usb, bytes.Replace(data, old, []byte("qdl->max_payload_size = 16384;"), 1), 0644); e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(source, "version.h"), []byte("#define VERSION \"microstick-read-"+qdlRevision+"\"\n"), 0644); e != nil {
		return "", e
	}
	flags, e := command("", nil, "pkg-config", "--cflags", "--libs", "libusb-1.0", "libxml-2.0")
	if e != nil {
		return "", e
	}
	args := []string{"-O2", "-D_FILE_OFFSET_BITS=64", "-I" + filepath.Join(source, "include"), "-I" + filepath.Join(source, "src"), "-I" + source}
	for _, name := range []string{"sahara", "util", "ux", "oscompat", "file", "file_nozip", "auto", "cli", "qud", "firehose", "input_type", "io", "patch", "program", "programmer", "read", "sahara_config", "sha2", "sim", "ufs", "usb", "vip", "sparse", "gpt", "flashmap", "json", "contents", "pathbuf", "qdl"} {
		args = append(args, filepath.Join(source, "src", name+".c"))
	}
	args = append(args, "-o", out)
	args = append(args, strings.Fields(string(flags))...)
	if e = os.MkdirAll(filepath.Dir(out), 0755); e != nil {
		return "", e
	}
	if _, e = command("", nil, "cc", args...); e != nil {
		return "", e
	}
	return out, nil
}

type rangeReader interface {
	Read(start, sectors uint64, dest string) error
}
type diskReader struct{ Path string }

func (r diskReader) Read(start, sectors uint64, dest string) error {
	f, e := os.Open(r.Path)
	if e != nil {
		return e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if start > uint64(st.Size()/512) || sectors > uint64(st.Size()/512)-start {
		return errors.New("read exceeds stock image")
	}
	if _, e = f.Seek(int64(start*512), io.SeekStart); e != nil {
		return e
	}
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.CopyN(out, f, int64(sectors*512))
	ce := out.Close()
	if e == nil {
		e = ce
	}
	return e
}

type qdlReader struct{ Tool, Programmer, Serial string }

func (r qdlReader) Read(start, sectors uint64, dest string) error {
	args := []string{"--backend=usb", "--storage=emmc", "--skip-reset"}
	if r.Serial != "" {
		args = append(args, "--serial="+r.Serial)
	}
	args = append(args, r.Programmer, "read", fmt.Sprintf("0/%d+%d", start, sectors), dest)
	_, e := command("", nil, r.Tool, args...)
	if e != nil {
		return e
	}
	st, e := os.Stat(dest)
	if e != nil {
		return e
	}
	if uint64(st.Size()) != sectors*512 {
		return errors.New("short EDL read")
	}
	return nil
}

type baseline struct {
	Files map[string]Entry `json:"files"`
	Guard map[string]any   `json:"factory_data_guard"`
}

// provisionLinux downloads and extracts the hash-pinned Linux build inputs.
func provisionLinux(p Paths, root, temp string, b baseline) error {
	archive, resource, e := resourceFile(p, "runtime-filesystem")
	if e != nil {
		return e
	}
	z, e := zip.OpenReader(archive)
	if e != nil {
		return e
	}
	defer z.Close()
	sparse := filepath.Join(temp, "linux-sparse.img")
	linuxBoot, _, e := resourceFile(p, "runtime-boot")
	if e != nil {
		return e
	}
	for _, pair := range [][2]string{{resource.Member, sparse}} {
		found := false
		for _, v := range z.File {
			if v.Name == pair[0] {
				if v.UncompressedSize64 > 2*1024*1024*1024 {
					return errors.New("upstream member too large")
				}
				src, e := v.Open()
				if e != nil {
					return e
				}
				dst, e := os.OpenFile(pair[1], os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if e != nil {
					src.Close()
					return e
				}
				_, e = io.Copy(dst, src)
				src.Close()
				dst.Close()
				if e != nil {
					return e
				}
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("pinned upstream archive member missing: %s", pair[0])
		}
	}
	rootfs := filepath.Join(temp, "linux-rootfs.ext4")
	if e = SparseToRaw(sparse, rootfs); e != nil {
		return e
	}
	boot, e := os.ReadFile(linuxBoot)
	if e != nil {
		return e
	}
	if len(boot) < 2048 || string(boot[:8]) != "ANDROID!" {
		return errors.New("invalid upstream boot")
	}
	page := binary.LittleEndian.Uint32(boot[36:])
	ks := binary.LittleEndian.Uint32(boot[8:])
	if uint64(page)+uint64(ks) > uint64(len(boot)) {
		return errors.New("truncated upstream kernel")
	}
	kernel := boot[page : page+ks]
	compressed := bytes.NewReader(kernel)
	gz, e := gzip.NewReader(compressed)
	if e != nil {
		return e
	}
	gz.Multistream(false)
	image, e := io.ReadAll(gz)
	gz.Close()
	if e != nil {
		return e
	}
	remaining := compressed.Len()
	dtb := kernel[len(kernel)-remaining:]
	if len(dtb) < 8 || binary.BigEndian.Uint32(dtb) != 0xd00dfeed {
		return errors.New("upstream appended DTB missing")
	}
	put := func(name string, data []byte) error {
		path := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			return e
		}
		return os.WriteFile(path, data, 0644)
	}
	if e = put("boot/kernel-with-dtb.bin", kernel); e != nil {
		return e
	}
	if e = put("boot/kernel.gz", kernel[:len(kernel)-remaining]); e != nil {
		return e
	}
	if e = put("device-tree/boot-image.dtb", dtb); e != nil {
		return e
	}
	if _, e = command("", nil, "dtc", "-I", "dtb", "-O", "dts", "-o", filepath.Join(root, "device-tree/boot-image.dts"), filepath.Join(root, "device-tree/boot-image.dtb")); e != nil {
		return e
	}
	// The running #17 configuration is embedded using Linux IKCONFIG magic.
	begin := bytes.Index(image, []byte("IKCFG_ST"))
	if begin < 0 {
		return errors.New("upstream kernel config missing")
	}
	end := bytes.Index(image[begin+8:], []byte("IKCFG_ED"))
	if end < 0 {
		return errors.New("upstream kernel config trailer missing")
	}
	cfg, e := gzip.NewReader(bytes.NewReader(image[begin+8 : begin+8+end]))
	if e != nil {
		return e
	}
	config, e := io.ReadAll(cfg)
	cfg.Close()
	if e != nil {
		return e
	}
	if e = put("kernel/running.config", config); e != nil {
		return e
	}
	// Layout comes from the official boot, not the owner's Android GPT. This
	// command prepares a build, not an installation or a repartitioning plan.
	cmd := strings.TrimRight(string(boot[64:576]), "\x00")
	match := regexp.MustCompile(`(?:^|\s)root=PARTUUID=([0-9a-fA-F-]{36})(?:\s|$)`).FindStringSubmatch(cmd)
	if len(match) != 2 {
		return errors.New("upstream boot root PARTUUID missing")
	}
	layout := BootLayout{fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(boot[12:])), fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(boot[20:])), fmt.Sprintf("0x%x", binary.LittleEndian.Uint32(boot[32:])), page, cmd, match[1]}
	if e = writeJSON(filepath.Join(root, "boot/layout.json"), layout, 0644); e != nil {
		return e
	}
	for name := range b.Files {
		if !strings.HasPrefix(name, "kernel/kernel-and-modules-tree/") {
			continue
		}
		remote := "/" + strings.TrimPrefix(name, "kernel/kernel-and-modules-tree/")
		data, e := imageBytes(rootfs, remote)
		if e != nil {
			return e
		}
		if e = put(name, data); e != nil {
			return e
		}
	}
	mbaArchive, mbaResource, e := resourceFile(p, "modem-loader")
	if e != nil {
		return e
	}
	firmwareZip, e := zip.OpenReader(mbaArchive)
	if e != nil {
		return e
	}
	defer firmwareZip.Close()
	foundMBA := false
	for _, v := range firmwareZip.File {
		if v.Name == mbaResource.Member {
			stream, e := v.Open()
			if e != nil {
				return e
			}
			mba, e := io.ReadAll(io.LimitReader(stream, 1024*1024))
			stream.Close()
			if e != nil {
				return e
			}
			if e = put("firmware/dsp/mba.mbn", mba); e != nil {
				return e
			}
			foundMBA = true
			break
		}
	}
	if !foundMBA {
		return errors.New("verified modem loader is missing from resource archive")
	}
	for name, expected := range b.Files {
		actual, e := entry(root, name)
		if e != nil {
			return e
		}
		if actual.Bytes != expected.Bytes || actual.SHA256 != expected.SHA256 {
			return fmt.Errorf("pinned Linux #17 payload mismatch: %s", name)
		}
	}
	return writeJSON(filepath.Join(root, "kernel/build-provenance.json"), map[string]any{"kernel_build": 17, "source": "hash-pinned microstick resource catalog", "kernel_dtb_modules_verified": true}, 0644)
}
func Extract(p Paths, reader rangeReader, output string) error {
	output, e := privatePath(p.Repo, output)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(output); e == nil {
		return errors.New("output exists; select a fresh external directory")
	}
	if e = os.MkdirAll(filepath.Dir(output), 0700); e != nil {
		return e
	}
	temp, e := os.MkdirTemp(filepath.Dir(output), ".extract-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	root := filepath.Join(temp, "bundle")
	if e = os.Mkdir(root, 0700); e != nil {
		return e
	}
	gptFile := filepath.Join(temp, "gpt.bin")
	if e = reader.Read(0, 34, gptFile); e != nil {
		return e
	}
	raw, e := os.ReadFile(gptFile)
	if e != nil {
		return e
	}
	gpt, e := ParseGPT(raw, 1)
	if e != nil {
		return e
	}
	if e = verifyGPTMirror(reader, raw, gpt, temp); e != nil {
		return e
	}
	parts := map[string]Partition{}
	for _, v := range gpt.Partitions {
		parts[v.Name] = v
	}
	for _, name := range []string{"modem", "persist", "boot"} {
		part, ok := parts[name]
		if !ok {
			return fmt.Errorf("expected original Android GPT partition %s; use an original own-device stock image or add a reviewed profile", name)
		}
		if part.Sectors == 0 || part.Sectors*512 > 128*1024*1024 {
			return errors.New("partition exceeds read-only extraction bounds")
		}
		if e = reader.Read(part.Start, part.Sectors, filepath.Join(temp, name+".img")); e != nil {
			return e
		}
	}
	stockHash, e := hashFile(filepath.Join(temp, "boot.img"))
	if e != nil {
		return e
	}

	bootBytes, e := os.ReadFile(filepath.Join(temp, "boot.img"))
	if e != nil {
		return e
	}
	_, variants, e := flasher.BootDTBs(bootBytes)
	if e != nil {
		return e
	}
	report := flasher.HardwareReport{Version: flasher.Schema, Device: flasher.DeviceInfo{Identity: flasher.Identity{Kind: "simulator", Hardware: "stock-image"}, Geometry: flasher.Geometry{SectorSize: 512, Areas: map[string]uint64{"user": (gpt.BackupLBA + 1) * 512}}}, Variants: variants}
	for _, part := range gpt.Partitions {
		report.GPT.Partitions = append(report.GPT.Partitions, flasher.Partition{Name: part.Name, Range: flasher.Range{Area: "user", Offset: part.Start * 512, Length: part.Sectors * 512}})
	}
	registry, e := flasher.NewRegistryClient(p.Work)
	if e != nil {
		return e
	}
	_ = registry.Ensure(context.Background())
	profiles := registry.Current().Profiles(flasher.EngineVersion, "", nil)
	resolution := flasher.Recognize(report, profiles)
	if resolution.Status != "matched" {
		return fmt.Errorf("stock hardware: %s: %s", resolution.Status, strings.Join(resolution.Reasons, "; "))
	}
	for i := range profiles {
		if profiles[i].ID == resolution.Profile {
			p.BoardProfile = &profiles[i]
			break
		}
	}
	if p.BoardProfile == nil {
		return errors.New("reviewed board profile unavailable")
	}
	var b baseline
	if e = json.Unmarshal(p.BoardProfile.RuntimeBundle(), &b); e != nil {
		return e
	}
	if e = provisionLinux(p, root, temp, b); e != nil {
		return e
	}

	dsp := DSP{Guard: b.Guard}
	mba, e := entry(root, "firmware/dsp/mba.mbn")
	if e != nil {
		return e
	}
	if mba.SHA256 != p.BoardProfile.Bundle["mba"] {
		return errors.New("runtime MBA differs from profile")
	}
	mba.Name = "mba.mbn"
	mba.Source = mba.Path
	mba.Path = ""
	dsp.Files = append(dsp.Files, mba)
	for _, family := range []struct {
		stem    string
		machine uint16
	}{{"modem", 164}, {"wcnss", 40}} {
		own, e := extractOwnPIL(filepath.Join(temp, "modem.img"), root, family.stem, family.machine)
		if e != nil {
			return e
		}
		dsp.Files = append(dsp.Files, own...)
	}
	guardMatches := false
	for _, f := range dsp.Files {
		if f.Name == dsp.Guard["firmware_file"] && f.SHA256 == dsp.Guard["firmware_sha256"] {
			guardMatches = true
		}
	}
	if !guardMatches {
		dsp.Guard = nil
	}
	nv, e := ownWiFiCalibration(filepath.Join(temp, "persist.img"))
	if e != nil {
		return e
	}
	nvRel := "firmware/firmware-tree/lib/firmware/wlan/prima/WCNSS_qcom_wlan_nv.bin"
	nvPath := filepath.Join(root, nvRel)
	if e = os.MkdirAll(filepath.Dir(nvPath), 0755); e != nil {
		return e
	}
	if e = os.WriteFile(nvPath, nv, 0600); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(root, "derived/firmware-map.json"), map[string]any{"files": []map[string]any{{"artifact_path": nvRel, "target_path": "/lib/firmware/wlan/prima/WCNSS_qcom_wlan_nv.bin", "component": "wifi-device-NV", "bytes": len(nv), "sha256": digest(nv), "device_specific": true}}}, 0644); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(root, "dsp-firmware.json"), dsp, 0644); e != nil {
		return e
	}
	if e = copyFile(filepath.Join(p.External, "board/msm8916/77-mm-qcom-soc.rules"), filepath.Join(root, "userspace/integration-tree/lib/udev/rules.d/77-mm-qcom-soc.rules"), 0644); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(root, "profile.json"), Profile{digest(nv)[:8], release, 17, "verified-captured-baseline"}, 0644); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(root, "original/partition-origins.json"), map[string]any{"factory_boot_sha256": stockHash, "partitions_read": []Partition{parts["modem"], parts["persist"], parts["boot"]}, "raw_nv_written": false, "transport": "EDL Firehose read, or own stock-image ranges", "kernel_module_source": "hash-pinned resource catalog"}, 0644); e != nil {
		return e
	}
	if e = copyFile(filepath.Join(temp, "boot.img"), filepath.Join(root, "original/factory-boot.img"), 0600); e != nil {
		return e
	}
	all, e := files(root)
	if e != nil {
		return e
	}
	var m Manifest
	m.Version = 2
	for _, name := range all {
		v, e := entry(root, name)
		if e != nil {
			return e
		}
		v.Source = "own stock device firmware/calibration; Linux #17+MBA from pinned upstream as recorded"
		m.Artifacts = append(m.Artifacts, v)
	}
	if e = writeJSON(filepath.Join(root, "MANIFEST.json"), m, 0644); e != nil {
		return e
	}
	if e = checksumSet(root); e != nil {
		return e
	}
	return os.Rename(root, output)
}
func ExtractCLI(p Paths, args []string) error {
	f := flag.NewFlagSet("extract-blobs", flag.ContinueOnError)
	stock := f.String("stock-image", "", "own original eMMC image, instead of a live device")
	output := f.String("output", p.Hardware, "new external hardware bundle directory")
	loader := f.String("programmer", "", "compatible MSM8916 programmer (default: download hash-pinned tested programmer)")
	serial := f.String("edl-serial", "", "optional QDL EDL serial when several devices are present")
	if e := f.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	var reader rangeReader
	if *stock != "" {
		reader = diskReader{*stock}
	} else {
		tool, e := ensureQDL(p)
		if e != nil {
			return e
		}
		devices, e := command("", nil, tool, "list")
		if e != nil {
			return e
		}
		count := 0
		for _, line := range strings.Split(string(devices), "\n") {
			if strings.HasPrefix(line, "05c6:9008") {
				count++
			}
		}
		if count == 0 {
			return errors.New("no EDL 05c6:9008 device; power off, hold the hardware button, connect USB, release after 1–2s; ADB is not required")
		}
		if count > 1 && *serial == "" {
			return errors.New("several EDL devices: select --edl-serial from QDL list")
		}
		programmer := *loader
		if programmer == "" {
			programmer = filepath.Join(p.Work, "buildroot-downloads/prog_emmc_firehose_8916.mbn")
			if e = Fetch(programmer, "https://raw.githubusercontent.com/bkerler/Loaders/6c416255056a178f54ccd7ee9b67d04a5bdfa8e6/qualcomm/factory/msm8916/007050e100000000_394a2e47cf830150_fhprg_peek.bin", programmerSHA); e != nil {
				return e
			}
		}
		h, e := hashFile(programmer)
		if e != nil {
			return e
		}
		if h != programmerSHA {
			return errors.New("programmer differs from this reviewed MSM8916 extraction profile")
		}
		reader = qdlReader{tool, programmer, *serial}
	}
	if e := Extract(p, reader, *output); e != nil {
		return e
	}
	fmt.Println("PASS: own stock blobs + pinned Linux inputs prepared; no partitions/NV/GPIO were written")
	return nil
}
