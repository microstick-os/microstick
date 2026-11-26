package dev

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type BootLayout struct {
	KernelAddress  string `json:"kernel_address_hex"`
	RamdiskAddress string `json:"ramdisk_address_hex"`
	TagsAddress    string `json:"tags_address_hex"`
	Page           uint32 `json:"page_size"`
	CommandLine    string `json:"cmdline"`
	RootUUID       string `json:"rootfs_partuuid"`
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		dest := filepath.Join(destination, rel)
		st, e := os.Lstat(path)
		if e != nil {
			return e
		}
		if d.IsDir() {
			return os.MkdirAll(dest, st.Mode().Perm())
		}
		if st.Mode()&os.ModeSymlink != 0 {
			value, e := os.Readlink(path)
			if e != nil {
				return e
			}
			os.Remove(dest)
			return os.Symlink(value, dest)
		}
		return copyFile(path, dest, st.Mode().Perm())
	})
}
func resolveTarget(root, path string) (string, error) {
	for n := 0; n < 40; n++ {
		if !within(path, root) {
			return "", errors.New("target symlink escapes filesystem")
		}
		st, e := os.Lstat(path)
		if e != nil {
			return "", e
		}
		if st.Mode()&os.ModeSymlink == 0 {
			return path, nil
		}
		link, e := os.Readlink(path)
		if e != nil {
			return "", e
		}
		if filepath.IsAbs(link) {
			path = filepath.Join(root, strings.TrimPrefix(link, "/"))
		} else {
			path = filepath.Clean(filepath.Join(filepath.Dir(path), link))
		}
	}
	return "", errors.New("target symlink loop")
}
func Initramfs(p Paths, rootUUID string) ([]byte, error) {
	root := filepath.Join(p.Output, "build/microstick-initramfs")
	if e := os.RemoveAll(root); e != nil {
		return nil, e
	}
	for _, dir := range []string{"bin", "sbin", "lib", "usr/lib", "usr/sbin", "dev", "proc", "sys", "newroot"} {
		if e := os.MkdirAll(filepath.Join(root, dir), 0755); e != nil {
			return nil, e
		}
	}
	index := map[string]string{}
	for _, dir := range []string{"lib", "usr/lib"} {
		entries, e := os.ReadDir(filepath.Join(p.Target, dir))
		if e != nil {
			return nil, e
		}
		for _, v := range entries {
			path, e := resolveTarget(p.Target, filepath.Join(p.Target, dir, v.Name()))
			if e != nil {
				continue
			}
			if st, e := os.Stat(path); e == nil && st.Mode().IsRegular() {
				index[v.Name()] = path
			}
		}
	}
	copied := map[string]bool{}
	var copyELF func(string, string) error
	copyELF = func(source, dest string) error {
		if copied[dest] {
			return nil
		}
		copied[dest] = true
		if e := copyFile(source, dest, 0755); e != nil {
			return e
		}
		f, e := elf.Open(source)
		if e != nil {
			return e
		}
		libs, e := f.ImportedLibraries()
		f.Close()
		if e != nil {
			return e
		}
		for _, lib := range libs {
			source, ok := index[lib]
			if !ok {
				return fmt.Errorf("missing initramfs dependency: %s", lib)
			}
			if e = copyELF(source, filepath.Join(root, "lib", lib)); e != nil {
				return e
			}
		}
		return nil
	}
	for _, name := range []string{"bin/busybox", "sbin/blkid", "sbin/e2fsck", "sbin/resize2fs", "sbin/dumpe2fs"} {
		source, e := resolveTarget(p.Target, filepath.Join(p.Target, name))
		if e != nil {
			return nil, e
		}
		if e = copyELF(source, filepath.Join(root, name)); e != nil {
			return nil, e
		}
	}
	loader, e := resolveTarget(p.Target, filepath.Join(p.Target, "lib/ld-musl-aarch64.so.1"))
	if e != nil {
		return nil, e
	}
	if e = copyFile(loader, filepath.Join(root, "lib/ld-musl-aarch64.so.1"), 0755); e != nil {
		return nil, e
	}
	if e = os.Symlink("busybox", filepath.Join(root, "bin/sh")); e != nil {
		return nil, e
	}
	for _, name := range []string{"mount", "umount", "mkdir", "sleep", "cat", "switch_root", "awk"} {
		if e = os.Symlink("busybox", filepath.Join(root, "bin", name)); e != nil {
			return nil, e
		}
	}
	script := `#!/bin/sh
export PATH=/bin:/sbin:/usr/bin:/usr/sbin
mount -t proc proc /proc
mount -t sysfs sysfs /sys
mount -t devtmpfs devtmpfs /dev
rootdev=
for count in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
    rootdev=$(blkid -t PARTUUID=ROOT_UUID -o device 2>/dev/null)
    [ -b "$rootdev" ] && break
    sleep 1
done
if [ -b "$rootdev" ]; then
    info=$(dumpe2fs -h "$rootdev" 2>/dev/null) || exec sh </dev/console >/dev/console 2>&1
    blocks=$(echo "$info" | awk '/^Block count:/ {print $3}')
    blocksize=$(echo "$info" | awk '/^Block size:/ {print $3}')
    sectors=$(cat "/sys/class/block/${rootdev##*/}/size")
    if [ "$((blocks * blocksize))" -lt "$((sectors * 512 / blocksize * blocksize))" ]; then
        result=0; e2fsck -p "$rootdev" || result=$?
        if [ "$result" -gt 1 ] || ! resize2fs "$rootdev"; then
            echo 'microstick: rootfs preparation failed; recovery shell' >/dev/console
            exec sh </dev/console >/dev/console 2>&1
        fi
    fi
fi
if [ -b "$rootdev" ] && mount -t ext4 -o rw,noatime "$rootdev" /newroot; then
    mount --move /dev /newroot/dev
    mount --move /sys /newroot/sys
    mount --move /proc /newroot/proc
    exec switch_root /newroot /sbin/init
fi
echo 'Buildroot: rootfs missing or unmountable; no storage changes performed.' >/dev/console
exec sh </dev/console >/dev/console 2>&1
`
	if e = os.WriteFile(filepath.Join(root, "init"), []byte(strings.ReplaceAll(script, "ROOT_UUID", rootUUID)), 0755); e != nil {
		return nil, e
	}
	return NewCPIO(root, epoch)
}
func NewCPIO(root string, stamp int64) ([]byte, error) {
	names := []string{"."}
	e := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path != root {
			rel, e := filepath.Rel(root, path)
			if e != nil {
				return e
			}
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	if e != nil {
		return nil, e
	}
	names = append(names, "TRAILER!!!")
	var raw bytes.Buffer
	align := func() {
		for raw.Len()%4 != 0 {
			raw.WriteByte(0)
		}
	}
	for i, name := range names {
		var data []byte
		mode := uint32(syscall.S_IFREG)
		if name != "TRAILER!!!" {
			path := filepath.Join(root, name)
			st, e := os.Lstat(path)
			if e != nil {
				return nil, e
			}
			mode = st.Sys().(*syscall.Stat_t).Mode
			if st.Mode()&os.ModeSymlink != 0 {
				link, e := os.Readlink(path)
				if e != nil {
					return nil, e
				}
				data = []byte(link)
			} else if st.Mode().IsRegular() {
				data, e = os.ReadFile(path)
				if e != nil {
					return nil, e
				}
			}
		}
		fields := []uint32{uint32(i + 1), mode, 0, 0, 1, uint32(stamp), uint32(len(data)), 0, 0, 0, 0, uint32(len(name) + 1), 0}
		raw.WriteString("070701")
		for _, v := range fields {
			fmt.Fprintf(&raw, "%08x", v)
		}
		raw.WriteString(name)
		raw.WriteByte(0)
		align()
		raw.Write(data)
		align()
	}
	var out bytes.Buffer
	gz, e := gzip.NewWriterLevel(&out, gzip.BestCompression)
	if e != nil {
		return nil, e
	}
	gz.Header.ModTime = time.Unix(stamp, 0)
	if _, e = gz.Write(raw.Bytes()); e != nil {
		return nil, e
	}
	if e = gz.Close(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func MakeBoot(kernel, ramdisk []byte, layout BootLayout) ([]byte, error) {
	if layout.Page != 2048 {
		return nil, errors.New("unsupported boot page size")
	}
	parse := func(s string) (uint32, error) { n, e := strconv.ParseUint(s, 0, 32); return uint32(n), e }
	ka, e := parse(layout.KernelAddress)
	if e != nil {
		return nil, e
	}
	ra, e := parse(layout.RamdiskAddress)
	if e != nil {
		return nil, e
	}
	tags, e := parse(layout.TagsAddress)
	if e != nil {
		return nil, e
	}
	cmd := []byte(layout.CommandLine + " rootwait rootfstype=ext4 ipv6.disable=1")
	if len(cmd) >= 512 {
		return nil, errors.New("boot command line too long")
	}
	id := sha1.New()
	for _, data := range [][]byte{kernel, ramdisk, nil} {
		id.Write(data)
		binary.Write(id, binary.LittleEndian, uint32(len(data)))
	}
	header := make([]byte, 1632)
	copy(header, []byte("ANDROID!"))
	values := []uint32{uint32(len(kernel)), ka, uint32(len(ramdisk)), ra, 0, 0, tags, layout.Page, 0, 0}
	for i, v := range values {
		binary.LittleEndian.PutUint32(header[8+i*4:], v)
	}
	copy(header[48:64], []byte("msm8916-microstick"))
	copy(header[64:576], cmd)
	copy(header[576:608], id.Sum(nil))
	var out bytes.Buffer
	for _, data := range [][]byte{header, kernel, ramdisk} {
		out.Write(data)
		for out.Len()%int(layout.Page) != 0 {
			out.WriteByte(0)
		}
	}
	if out.Len() > 64*1024*1024 {
		return nil, errors.New("boot image exceeds partition bound")
	}
	return out.Bytes(), nil
}
func filesystemAllocated(image string) (int64, error) {
	out, e := command("", nil, "dumpe2fs", "-h", image)
	if e != nil {
		return 0, e
	}
	values := map[string]int64{}
	for _, line := range strings.Split(string(out), "\n") {
		s := strings.SplitN(line, ":", 2)
		if len(s) == 2 {
			n, e := strconv.ParseInt(strings.TrimSpace(s[1]), 10, 64)
			if e == nil {
				values[s[0]] = n
			}
		}
	}
	if values["Block size"] <= 0 || values["Block count"] <= 0 {
		return 0, errors.New("invalid ext4 geometry")
	}
	return (values["Block count"] - values["Free blocks"]) * values["Block size"], nil
}
func PostImage(p Paths, images string) error {
	var layout BootLayout
	if e := readJSON(filepath.Join(p.Hardware, "boot/layout.json"), &layout); e != nil {
		return e
	}
	ramdisk, e := Initramfs(p, layout.RootUUID)
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(images, "initramfs.cpio.gz"), ramdisk, 0644); e != nil {
		return e
	}
	kernel, e := os.ReadFile(filepath.Join(p.Hardware, "boot/kernel-with-dtb.bin"))
	if e != nil {
		return e
	}
	boot, e := MakeBoot(kernel, ramdisk, layout)
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(images, "boot.img"), boot, 0644); e != nil {
		return e
	}
	if _, e = command("", nil, "e2fsck", "-fn", filepath.Join(images, "rootfs.ext4")); e != nil {
		return e
	}
	allocated, e := filesystemAllocated(filepath.Join(images, "rootfs.ext4"))
	if e != nil {
		return e
	}
	if allocated > 128*1024*1024 {
		return errors.New("rootfs used blocks exceed 128MiB")
	}
	destination := p.Artifacts
	if e = os.MkdirAll(filepath.Dir(destination), 0700); e != nil {
		return e
	}
	p.Artifacts, e = os.MkdirTemp(filepath.Dir(destination), ".base-images-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(p.Artifacts)
	for _, name := range []string{"boot.img", "rootfs.ext4", "rootfs.tar", "initramfs.cpio.gz"} {
		if e = copyFile(filepath.Join(images, name), filepath.Join(p.Artifacts, name), 0600); e != nil {
			return e
		}
	}
	for _, pair := range [][2]string{{filepath.Join(p.Output, ".config"), "buildroot.config"}, {filepath.Join(p.Work, "buildroot-private/credentials.json"), "credentials.json"}} {
		if e = copyFile(pair[0], filepath.Join(p.Artifacts, pair[1]), 0600); e != nil {
			return e
		}
	}
	configs, _ := filepath.Glob(filepath.Join(p.Output, "build/busybox-*/.config"))
	if len(configs) != 1 {
		return errors.New("expected one busybox config")
	}
	if e = copyFile(configs[0], filepath.Join(p.Artifacts, "busybox.config"), 0644); e != nil {
		return e
	}
	if e = os.RemoveAll(filepath.Join(p.Artifacts, "provenance")); e != nil {
		return e
	}
	if e = copyTree(filepath.Join(p.Target, "usr/share/microstick"), filepath.Join(p.Artifacts, "provenance")); e != nil {
		return e
	}
	data, e := command("", nil, "make", "-s", "--no-print-directory", "-C", filepath.Join(p.Work, "buildroot"), "O="+p.Output, "BR2_EXTERNAL="+p.External, "show-info")
	if e != nil {
		return e
	}
	if !json.Valid(data) {
		return errors.New("invalid Buildroot package metadata")
	}
	if e = os.WriteFile(filepath.Join(p.Artifacts, "packages.json"), data, 0644); e != nil {
		return e
	}
	var sources []Entry
	sourceRoot := filepath.Join(p.Work, "buildroot-downloads")
	names, e := files(sourceRoot)
	if e != nil {
		return e
	}
	for _, name := range names {
		if strings.Contains(name, ".tar.") || strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".patch") || strings.HasSuffix(name, ".diff") {
			v, e := entry(sourceRoot, name)
			if e != nil {
				return e
			}
			sources = append(sources, v)
		}
	}
	if e = writeJSON(filepath.Join(p.Artifacts, "sources.json"), map[string]any{"files": sources, "scope": "hash-pinned source download cache; generated files outside repository"}, 0644); e != nil {
		return e
	}
	if e = ArchiveSources(p, epoch); e != nil {
		return e
	}
	text := "# microstick private base images\n\nPrivate device-specific base bundle. Do not publish credentials or another unit's calibration.\nThe full public sources/docs are in microstick-source.tar.gz.\n\nBase images contain no default IMEI. Use prepare-install from the source tree, a new\nprivate installation record for each new install, and a separate output directory.\nRetry reuses the record. The flasher derives the installation profile from device data or selects a catalog recipe.\n\nRead-only integrity check: sha256sum -c SHA256SUMS\n"
	if e = os.WriteFile(filepath.Join(p.Artifacts, "README.md"), []byte(text), 0644); e != nil {
		return e
	}
	var profile Profile
	if e = readJSON(filepath.Join(p.Hardware, "profile.json"), &profile); e != nil {
		return e
	}
	if e = finalize(p.Artifacts, Manifest{Version: 2, Device: profile.DeviceID, Capacity: 256 * 1024 * 1024, Allocated: allocated}); e != nil {
		return e
	}
	return replaceBundle(p.Artifacts, destination)
}

// Publish a fully prepared bundle. Reports from the preceding build cannot enter
// the new manifest. A failed rename restores the previous bundle.
func replaceBundle(staged, destination string) error {
	previous, e := os.MkdirTemp(filepath.Dir(destination), ".previous-base-")
	if e != nil {
		return e
	}
	if e = os.Remove(previous); e != nil {
		return e
	}
	hadPrevious := false
	if _, e = os.Lstat(destination); e == nil {
		if e = os.Rename(destination, previous); e != nil {
			return e
		}
		hadPrevious = true
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = os.Rename(staged, destination); e != nil {
		if hadPrevious {
			if restore := os.Rename(previous, destination); restore != nil {
				return fmt.Errorf("publish failed: %w; previous bundle retained at %s: %v", e, previous, restore)
			}
		}
		return e
	}
	if hadPrevious {
		return os.RemoveAll(previous)
	}
	return nil
}
func cpioNames(data []byte) ([]string, error) {
	var names []string
	offset := 0
	for offset < len(data) {
		if offset+110 > len(data) || string(data[offset:offset+6]) != "070701" {
			return nil, errors.New("bad newc header")
		}
		size, e := strconv.ParseUint(string(data[offset+54:offset+62]), 16, 32)
		if e != nil {
			return nil, e
		}
		length, e := strconv.ParseUint(string(data[offset+94:offset+102]), 16, 32)
		if e != nil || length == 0 || length > 4096 {
			return nil, errors.New("bad newc name")
		}
		start := offset + 110
		end := start + int(length)
		if end > len(data) || data[end-1] != 0 {
			return nil, errors.New("truncated newc name")
		}
		name := string(data[start : end-1])
		names = append(names, name)
		offset = (end + 3) &^ 3
		if size > uint64(len(data)-offset) {
			return nil, errors.New("truncated newc payload")
		}
		offset = (offset + int(size) + 3) &^ 3
		if name == "TRAILER!!!" {
			return names, nil
		}
	}
	return nil, errors.New("newc trailer missing")
}
func hexData(data []byte) string { return hex.EncodeToString(data) }
