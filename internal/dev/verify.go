package dev

import (
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

func imageBytes(image, name string) ([]byte, error) {
	return command("", nil, "debugfs", "-R", "cat /"+strings.TrimPrefix(name, "/"), image)
}
func Verify(p Paths) error {
	m, e := verifyManifest(p.Artifacts)
	if e != nil {
		return e
	}
	if m.Personalized {
		return errors.New("offline build verifier expects an unpersonalized base")
	}
	checks := []string{"artifact manifest sizes/SHA256"}
	image := filepath.Join(p.Artifacts, "rootfs.ext4")
	if _, e = command(p.Artifacts, nil, "sha256sum", "-c", "SHA256SUMS"); e != nil {
		return e
	}
	var hardware Manifest
	if e = readJSON(filepath.Join(p.Hardware, "MANIFEST.json"), &hardware); e != nil {
		return e
	}
	if e = verifyEntries(p.Hardware, hardware.Artifacts); e != nil {
		return e
	}
	var dsp DSP
	if e = readJSON(filepath.Join(p.Hardware, "dsp-firmware.json"), &dsp); e != nil {
		return e
	}
	expected := map[string]bool{}
	for _, item := range dsp.Files {
		name := "lib/firmware/" + item.Name
		expected[item.Name] = true
		actual, e := entry(p.Target, name)
		if e != nil {
			return e
		}
		data, e := imageBytes(image, name)
		if e != nil {
			return e
		}
		if actual.Bytes != item.Bytes || actual.SHA256 != item.SHA256 || digest(data) != item.SHA256 {
			return fmt.Errorf("DSP mismatch: %s", name)
		}
	}
	for _, pattern := range []string{"modem.*", "wcnss.*"} {
		names, _ := filepath.Glob(filepath.Join(p.Target, "lib/firmware", pattern))
		for _, name := range names {
			if !expected[filepath.Base(name)] {
				return errors.New("stale DSP segments in target")
			}
		}
	}
	checks = append(checks, "exact hardware source hashes and matching MBA/MPSS/WCNSS in target/image")
	boot, e := os.ReadFile(filepath.Join(p.Artifacts, "boot.img"))
	if e != nil {
		return e
	}
	if len(boot) < 2048 || string(boot[:8]) != "ANDROID!" {
		return errors.New("invalid Android boot header")
	}
	page := binary.LittleEndian.Uint32(boot[36:])
	ks := binary.LittleEndian.Uint32(boot[8:])
	rs := binary.LittleEndian.Uint32(boot[16:])
	if page != 2048 || binary.LittleEndian.Uint32(boot[12:]) != 0x80080000 || binary.LittleEndian.Uint32(boot[20:]) != 0x82000000 || binary.LittleEndian.Uint32(boot[32:]) != 0x81e00000 {
		return errors.New("Android boot addresses/page mismatch")
	}
	end := uint64(page) + uint64(ks)
	if end > uint64(len(boot)) {
		return errors.New("truncated boot kernel")
	}
	kernel, e := os.ReadFile(filepath.Join(p.Hardware, "boot/kernel-with-dtb.bin"))
	if e != nil {
		return e
	}
	if !bytes.Equal(kernel, boot[page:end]) {
		return errors.New("kernel #17/DTB byte mismatch")
	}
	start := (end + uint64(page) - 1) / uint64(page) * uint64(page)
	if start+uint64(rs) > uint64(len(boot)) {
		return errors.New("truncated boot ramdisk")
	}
	var layout BootLayout
	if e = readJSON(filepath.Join(p.Hardware, "boot/layout.json"), &layout); e != nil {
		return e
	}
	rebuilt, e := MakeBoot(kernel, boot[start:start+uint64(rs)], layout)
	if e != nil {
		return e
	}
	if !bytes.Equal(rebuilt, boot) {
		return errors.New("boot header, command line, SHA1 ID or padding mismatch")
	}
	standalone, e := os.ReadFile(filepath.Join(p.Artifacts, "initramfs.cpio.gz"))
	if e != nil {
		return e
	}
	if !bytes.Equal(standalone, boot[start:start+uint64(rs)]) {
		return errors.New("boot/standalone initramfs mismatch")
	}
	gz, e := gzip.NewReader(bytes.NewReader(boot[start : start+uint64(rs)]))
	if e != nil {
		return e
	}
	raw, e := io.ReadAll(gz)
	gz.Close()
	if e != nil {
		return e
	}
	names, e := cpioNames(raw)
	if e != nil {
		return e
	}
	set := map[string]bool{}
	for _, name := range names {
		set[name] = true
	}
	for _, name := range []string{"init", "bin/busybox", "sbin/blkid", "sbin/e2fsck", "sbin/resize2fs", "sbin/dumpe2fs", "lib/ld-musl-aarch64.so.1", "TRAILER!!!"} {
		if !set[name] {
			return fmt.Errorf("initramfs entry missing: %s", name)
		}
	}
	checks = append(checks, "Android v0, exact kernel #17/DTB, complete newc and gzip integrity")
	if _, e = command("", nil, "e2fsck", "-fn", image); e != nil {
		return e
	}
	checks = append(checks, "e2fsck -fn")
	targetFiles, e := files(p.Target)
	if e != nil {
		return e
	}
	libs := map[string]bool{}
	for _, dir := range []string{"lib", "usr/lib", "usr/share/microstick/default-plugins/web/lib"} {
		entries, _ := os.ReadDir(filepath.Join(p.Target, dir))
		for _, v := range entries {
			libs[v.Name()] = true
		}
	}
	elfCount, payloadCount := 0, 0
	for _, name := range targetFiles {
		path := filepath.Join(p.Target, name)
		if !strings.HasPrefix(name, "lib/firmware/") {
			f, e := os.Open(path)
			if e != nil {
				return e
			}
			header := make([]byte, 4)
			_, _ = f.Read(header)
			f.Close()
			if bytes.Equal(header, []byte{0x7f, 'E', 'L', 'F'}) {
				binaryFile, e := elf.Open(path)
				if e != nil {
					return e
				}
				if binaryFile.Machine != elf.EM_AARCH64 {
					binaryFile.Close()
					return fmt.Errorf("non-AArch64 ELF: %s", name)
				}
				needed, e := binaryFile.ImportedLibraries()
				binaryFile.Close()
				if e != nil {
					return e
				}
				for _, lib := range needed {
					if !libs[lib] {
						return fmt.Errorf("unresolved %s: %s", name, lib)
					}
				}
				elfCount++
			}
		}
		if strings.HasPrefix(name, "usr/libexec/") || strings.HasPrefix(name, "usr/share/microstick/") || strings.HasPrefix(name, "lib/modules/") || strings.HasPrefix(name, "etc/microstick/") || strings.HasPrefix(name, "etc/init.d/") || name == "etc/shadow" || name == "etc/avahi/services/microstick.service" {
			expected, e := os.ReadFile(path)
			if e != nil {
				return e
			}
			actual, e := imageBytes(image, name)
			if e != nil {
				return e
			}
			matches := bytes.Equal(expected, actual)
			if name == "etc/shadow" {
				// Buildroot's fakeroot user table adds service accounts only to
				// the image. Every pre-existing target row must remain exact.
				matches = true
				for _, row := range strings.Split(strings.TrimSpace(string(expected)), "\n") {
					if !bytes.Contains(append([]byte{'\n'}, actual...), []byte("\n"+row+"\n")) {
						matches = false
					}
				}
			}
			if !matches {
				return fmt.Errorf("target/image payload mismatch: %s", name)
			}
			payloadCount++
		}
		if strings.HasSuffix(name, "cfg80211.ko") {
			if e = verifyRegDB(p, path); e != nil {
				return e
			}
		}
		if (strings.Contains(name, "default-plugins/statistics/") && (strings.Contains(name, "lte-") || strings.Contains(name, "wifi-")) && strings.HasSuffix(name, ".bin")) || strings.HasSuffix(name, "/sessions.json") {
			return errors.New("base contains seeded durable user state")
		}
	}
	checks = append(checks, fmt.Sprintf("%d AArch64 ELF files and dependency closure", elfCount), fmt.Sprintf("%d target/image payloads match", payloadCount), "regdb signature matches the cfg80211 certificate")
	modules := filepath.Join(p.Target, "lib/modules", release)
	selected, e := ModuleSelection(filepath.Join(p.Hardware, "kernel/kernel-and-modules-tree/lib/modules", release))
	if e != nil {
		return e
	}
	for _, name := range selected {
		a, e := hashFile(filepath.Join(modules, name))
		if e != nil {
			return e
		}
		b, e := hashFile(filepath.Join(p.Hardware, "kernel/kernel-and-modules-tree/lib/modules", release, name))
		if e != nil {
			return e
		}
		if a != b {
			return errors.New("kernel module ABI/hash mismatch")
		}
	}
	value, e := imageDefault(image)
	if e != nil {
		return e
	}
	if value != "" {
		return errors.New("base has fixed default IMEI")
	}
	for _, name := range []string{"usr/share/microstick/default-imei.json", "etc/microstick/imei.json", "usr/libexec/microstick-snapshot", "etc/init.d/S50avahi-daemon", "usr/bin/python3", "usr/bin/node", "usr/bin/nmcli", "usr/sbin/NetworkManager", "etc/microstick/lan.json", "etc/microstick/firewall.json"} {
		if _, e = os.Stat(filepath.Join(p.Target, name)); e == nil {
			return fmt.Errorf("unexpected base file %s", name)
		}
	}
	passwd, e := imageBytes(image, "etc/passwd")
	if e != nil {
		return e
	}
	if !strings.Contains(string(passwd), "avahi:") {
		return errors.New("image has no Avahi account")
	}
	shadow, e := imageBytes(image, "etc/shadow")
	if e != nil {
		return e
	}
	if !strings.HasPrefix(string(shadow), "root:$6$") {
		return errors.New("root password hash overwritten")
	}
	var credentials Credentials
	if e = readJSON(filepath.Join(p.Artifacts, "credentials.json"), &credentials); e != nil {
		return e
	}
	privatePath := filepath.Join(p.Work, "buildroot-private/credentials.json")
	privateHash, e := hashFile(privatePath)
	if e != nil {
		return e
	}
	artifactHash, e := hashFile(filepath.Join(p.Artifacts, "credentials.json"))
	if e != nil {
		return e
	}
	if artifactHash != privateHash {
		return errors.New("build did not preserve private credentials")
	}
	passwordHash, e := command("", []byte(credentials.RootPassword+"\n"), "openssl", "passwd", "-6", "-salt", privateHash[:16], "-stdin")
	if e != nil {
		return e
	}
	if !bytes.HasPrefix(shadow, []byte("root:"+strings.TrimSpace(string(passwordHash))+":")) {
		return errors.New("image root password differs from private credentials")
	}
	st, e := os.Stat(filepath.Join(p.Artifacts, "credentials.json"))
	if e != nil {
		return e
	}
	if st.Mode().Perm()&0077 != 0 {
		return errors.New("credential file is not private")
	}
	psk := hex.EncodeToString(pbkdf2([]byte(credentials.WiFiPassword), []byte(credentials.SSID), 4096, 32, sha1.New))
	hostapd, e := os.ReadFile(filepath.Join(p.Target, "etc/microstick/hostapd.conf"))
	if e != nil {
		return e
	}
	if !bytes.Contains(hostapd, []byte("wpa_psk="+psk+"\n")) {
		return errors.New("WPA2 PSK mismatch")
	}
	authPath := filepath.Join(p.Target, "usr/share/microstick/default-plugins/web/default-auth.json")
	var auth map[string]string
	if e = readJSON(authPath, &auth); e != nil {
		return e
	}
	if auth["hash"] != hex.EncodeToString(pbkdf2([]byte(credentials.WiFiPassword), []byte(auth["salt"]), 120000, 32, sha256.New)) {
		return errors.New("panel password hash mismatch")
	}
	checks = append(checks, "WPA2/panel password derivation, root hash, private credentials, empty durable state, base without fixed IMEI")
	versions := map[string]string{}
	commands := map[string][]string{"e2fsck": {"/sbin/e2fsck", "-V"}, "resize2fs": {"/sbin/resize2fs", "-V"}, "mke2fs": {"/sbin/mke2fs", "-V"}, "routerd": {"/usr/libexec/microstick-routerd", "--version"}, "networkd": {"/usr/libexec/microstick-networkd", "--version"}, "avahi": {"/usr/sbin/avahi-daemon", "--version"}, "busybox": {"/bin/busybox"}, "modemmanager": {"/usr/sbin/ModemManager", "--version"}, "mmcli": {"/usr/bin/mmcli", "--version"}, "curl": {"/usr/bin/curl", "--version"}, "dropbear": {"/usr/sbin/dropbear", "-V"}, "jq": {"/usr/bin/jq", "--version"}, "dnsmasq": {"/usr/sbin/dnsmasq", "--version"}, "hostapd": {"/usr/sbin/hostapd", "-v"}, "wpa_supplicant": {"/usr/sbin/wpa_supplicant", "-v"}, "htop": {"/usr/bin/htop", "--version"}, "iproute2": {"/sbin/ip", "-V"}, "iptables": {"/usr/sbin/iptables", "-V"}}
	emulator := []string{"-L", p.Target, "-E", "LD_LIBRARY_PATH=" + filepath.Join(p.Target, "lib") + ":" + filepath.Join(p.Target, "usr/lib")}
	for name, args := range commands {
		a := append(append([]string(nil), emulator...), filepath.Join(p.Target, strings.TrimPrefix(args[0], "/")))
		a = append(a, args[1:]...)
		c := exec.Command(filepath.Join(p.Host, "bin/qemu-aarch64"), a...)
		out, e := c.CombinedOutput()
		if e != nil {
			exit, ok := e.(*exec.ExitError)
			if !(ok && (name == "hostapd" || name == "resize2fs") && exit.ExitCode() == 1) {
				return fmt.Errorf("QEMU startup %s: %w", name, e)
			}
		}
		if len(out) > 1600 {
			out = out[:1600]
		}
		versions[name] = strings.TrimSpace(string(out))
	}
	request, _ := json.Marshal(map[string]string{"ssid": "Русская сеть", "security": "wpa2", "password": "synthetic-check-only"})
	a := append(append([]string(nil), emulator...), filepath.Join(p.Target, "usr/libexec/microstick-wifi"), "config")
	out, e := command("", request, filepath.Join(p.Host, "bin/qemu-aarch64"), a...)
	if e != nil {
		return e
	}
	if !bytes.Contains(out, []byte("ssid="+hex.EncodeToString([]byte("Русская сеть")))) || bytes.Contains(out, []byte("synthetic-check-only")) {
		return errors.New("target SSID/PSK codec mismatch")
	}
	checks = append(checks, "18 target tools start under QEMU; actual target SSID codec and secret-free WPA2 config")
	for _, relative := range []string{"usr/share/microstick/default-plugins/web/run", "usr/libexec/microstick-router"} {
		out, e := command("", nil, "debugfs", "-R", "stat /"+relative, image)
		if e != nil {
			return e
		}
		if !regexp.MustCompile(`User:\s+0\s+Group:\s+0`).Match(out) {
			return errors.New("image payload ownership mismatch")
		}
	}
	cflags := exec.Command(filepath.Join(p.Host, "bin/pkg-config"), "--cflags", "libpcre2-8")
	cflags.Env = append(os.Environ(), "PKG_CONFIG_SYSROOT_DIR=/", "PKG_CONFIG_LIBDIR="+filepath.Join(p.Host, "lib/pkgconfig"))
	out, e = cflags.Output()
	if e != nil {
		return e
	}
	if !bytes.Contains(out, []byte("-I"+filepath.Join(p.Host, "include"))) {
		return errors.New("host pkgconf lost UTF-8 workspace path")
	}
	checks = append(checks, "root-owned payload and UTF-8 host pkgconf path")
	imageHashes := map[string]string{}
	for _, name := range []string{"boot.img", "rootfs.ext4", "microstick-source.tar.gz"} {
		imageHashes[name], e = hashFile(filepath.Join(p.Artifacts, name))
		if e != nil {
			return e
		}
	}
	if e = writeJSON(filepath.Join(p.Artifacts, "verification.json"), map[string]any{"status": "PASS", "hardware_tested": false, "checks": checks, "versions": versions, "elf_files": elfCount, "payload_files": payloadCount, "verifier": "native Go", "verified_artifacts": imageHashes}, 0644); e != nil {
		return e
	}
	if e = finalize(p.Artifacts, m); e != nil {
		return e
	}
	fmt.Printf("PASS: %d offline checks, %d target/image payloads, %d AArch64 ELFs; no hardware accessed\n", len(checks), payloadCount, elfCount)
	return nil
}
func verifyRegDB(p Paths, module string) error {
	data, e := os.ReadFile(module)
	if e != nil {
		return e
	}
	var certificate []byte
	for pos := 0; pos+4 < len(data); pos++ {
		if data[pos] != 0x30 || data[pos+1] != 0x82 {
			continue
		}
		size := int(binary.BigEndian.Uint16(data[pos+2:])) + 4
		if size > 4096 || pos+size > len(data) {
			continue
		}
		candidate := data[pos : pos+size]
		cert, e := x509.ParseCertificate(candidate)
		if e == nil && strings.Contains(cert.Subject.String(), "sforshee") {
			certificate = candidate
			break
		}
	}
	if certificate == nil {
		return errors.New("cfg80211 regulatory certificate not found")
	}
	dir, e := os.MkdirTemp("", "microstick-regdb-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "certificate.pem")
	if e = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate}), 0600); e != nil {
		return e
	}
	_, e = command("", nil, "openssl", "cms", "-verify", "-binary", "-inform", "DER", "-in", filepath.Join(p.Target, "lib/firmware/regulatory.db.p7s"), "-content", filepath.Join(p.Target, "lib/firmware/regulatory.db"), "-certfile", path, "-noverify", "-out", "/dev/null")
	return e
}
