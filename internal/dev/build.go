package dev

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const release = "5.15.0-handsomekernel+"
const epoch int64 = 1793577600

var bigTen = big.NewInt(10)

type Profile struct {
	DeviceID      string `json:"device_id"`
	KernelRelease string `json:"kernel_release"`
	KernelBuild   int    `json:"kernel_build"`
	Compatibility string `json:"compatibility"`
}
type DSP struct {
	Files []Entry        `json:"files"`
	Guard map[string]any `json:"factory_data_guard"`
}
type FirmwareMap struct {
	Files []struct {
		Artifact  string `json:"artifact_path"`
		Target    string `json:"target_path"`
		Component string `json:"component"`
	} `json:"files"`
}
type Credentials struct {
	SSID         string `json:"ssid"`
	WiFiPassword string `json:"wifi_password"`
	RootPassword string `json:"root_password"`
	Branded      bool   `json:"microstick_v1"`
}

func Fetch(path, url, expected string) error {
	if _, e := os.Stat(path); e == nil {
		h, e := hashFile(path)
		if e != nil {
			return e
		}
		if h != expected {
			return fmt.Errorf("cached download checksum mismatch: %s", filepath.Base(path))
		}
		return nil
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	client := http.Client{Timeout: 5 * time.Minute}
	response, e := client.Get(url)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP%d: %s", response.StatusCode, url)
	}
	temp, e := os.CreateTemp(filepath.Dir(path), ".download-")
	if e != nil {
		return e
	}
	defer os.Remove(temp.Name())
	h := sha256.New()
	_, e = io.Copy(io.MultiWriter(temp, h), response.Body)
	ce := temp.Close()
	if e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if hex.EncodeToString(h.Sum(nil)) != expected {
		return fmt.Errorf("download checksum mismatch: %s", filepath.Base(path))
	}
	return os.Rename(temp.Name(), path)
}
func Bootstrap(p Paths) error {
	archive := filepath.Join(p.Work, "buildroot-downloads/buildroot-2025.02.19.tar.xz")
	if e := Fetch(archive, "https://buildroot.org/downloads/buildroot-2025.02.19.tar.xz", "e5e58bff1da91f022f60cefdfe280140c08023cd0aeec1e5f6f9da0ea866d69c"); e != nil {
		return e
	}
	if _, e := os.Stat(filepath.Join(p.Work, "buildroot")); os.IsNotExist(e) {
		if _, e = command("", nil, "tar", "-xf", archive, "-C", filepath.Join(p.Work, "buildroot-downloads")); e != nil {
			return e
		}
		if e = os.Rename(filepath.Join(p.Work, "buildroot-downloads/buildroot-2025.02.19"), filepath.Join(p.Work, "buildroot")); e != nil {
			return e
		}
	}
	if _, e := command("", []byte("2^10\n"), "bc", "-l"); e == nil {
		return nil
	}
	bc := filepath.Join(p.Work, "host-tools/bin/bc")
	if _, e := os.Stat(bc); e == nil {
		return nil
	}
	a := filepath.Join(p.Work, "buildroot-downloads/bc-1.07.1.tar.gz")
	if e := Fetch(a, "https://ftp.gnu.org/gnu/bc/bc-1.07.1.tar.gz", "62adfca89b0a1c0164c2cdca59ca210c1d44c3ffc46daf9931cf4942664cb02a"); e != nil {
		return e
	}
	source := filepath.Join(p.Work, "host-tools/src")
	if e := os.MkdirAll(source, 0755); e != nil {
		return e
	}
	if _, e := command("", nil, "tar", "-xf", a, "-C", source); e != nil {
		return e
	}
	source = filepath.Join(source, "bc-1.07.1")
	if _, e := command(source, nil, "./configure", "--without-readline", "--without-libedit", "CFLAGS=-O2 -std=gnu17"); e != nil {
		return e
	}
	data, e := os.ReadFile(filepath.Join(p.Work, "buildroot/package/bc/0004-no-gen-libmath.patch"))
	if e != nil {
		return e
	}
	parts := strings.Split(string(data), "+++ b/bc/libmath.h\n")
	if len(parts) != 2 {
		return errors.New("bc libmath patch format changed")
	}
	var header strings.Builder
	for _, line := range strings.Split(strings.Split(parts[1], "-- \n")[0], "\n") {
		if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
			header.WriteString(line[1:] + "\n")
		}
	}
	if e = os.WriteFile(filepath.Join(source, "bc/libmath.h"), []byte(header.String()), 0644); e != nil {
		return e
	}
	makefile := filepath.Join(source, "bc/Makefile")
	data, e = os.ReadFile(makefile)
	if e != nil {
		return e
	}
	rule := regexp.MustCompile(`(?m)^libmath\.h:.*\n(?:\t.*\n)+`)
	if !rule.Match(data) {
		return errors.New("bc generator rule missing")
	}
	if e = os.WriteFile(makefile, rule.ReplaceAll(data, nil), 0644); e != nil {
		return e
	}
	for _, args := range [][]string{{"-j8", "-C", "lib"}, {"-j8", "-C", "bc", "bc"}} {
		if _, e = command(source, nil, "make", args...); e != nil {
			return e
		}
	}
	return copyFile(filepath.Join(source, "bc/bc"), bc, 0755)
}
func pbkdf2(password, salt []byte, iterations, length int, newHash func() hash.Hash) []byte {
	var result []byte
	for block := 1; len(result) < length; block++ {
		mac := hmac.New(newHash, password)
		mac.Write(salt)
		mac.Write([]byte{byte(block >> 24), byte(block >> 16), byte(block >> 8), byte(block)})
		u := mac.Sum(nil)
		sum := append([]byte(nil), u...)
		for i := 1; i < iterations; i++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(nil)
			for k := range sum {
				sum[k] ^= u[k]
			}
		}
		result = append(result, sum...)
	}
	return result[:length]
}
func ModuleSelection(dir string) ([]string, error) {
	data, e := os.ReadFile(filepath.Join(dir, "modules.dep"))
	if e != nil {
		return nil, e
	}
	deps := map[string][]string{}
	byName := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		s := strings.SplitN(line, ":", 2)
		if len(s) != 2 {
			return nil, errors.New("invalid modules.dep")
		}
		deps[s[0]] = strings.Fields(s[1])
		byName[strings.ReplaceAll(strings.TrimSuffix(filepath.Base(s[0]), ".ko"), "-", "_")] = s[0]
	}
	selected := map[string]bool{}
	var add func(string) error
	add = func(name string) error {
		if selected[name] {
			return nil
		}
		children, ok := deps[name]
		if !ok {
			return fmt.Errorf("missing module %s", name)
		}
		selected[name] = true
		for _, c := range children {
			if e := add(c); e != nil {
				return e
			}
		}
		return nil
	}
	for _, name := range []string{"qcom_q6v5_mss", "qcom_wcnss_pil", "wcn36xx", "rpmsg_wwan_ctrl", "qcom_bam_dmux", "iptable_filter", "iptable_nat", "iptable_mangle", "xt_conntrack", "xt_MASQUERADE", "xt_mac", "xt_REDIRECT", "xt_TCPMSS", "xt_tcpudp", "xt_LOG", "nf_tables", "nft_counter", "ccm", "cmac", "ipv6"} {
		n, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("required module missing: %s", name)
		}
		if e = add(n); e != nil {
			return nil, e
		}
	}
	for _, name := range []string{"aes_neon_blk", "aes_neon_bs", "sch_fq_codel", "nf_log_syslog", "crc32c_generic", "crc32c"} {
		if n, ok := byName[name]; ok {
			if e = add(n); e != nil {
				return nil, e
			}
		}
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
func PostBuild(p Paths, target string) error {
	var hardware Manifest
	if e := readJSON(filepath.Join(p.Hardware, "MANIFEST.json"), &hardware); e != nil {
		return e
	}
	if e := verifyEntries(p.Hardware, hardware.Artifacts); e != nil {
		return e
	}
	var profile Profile
	if e := readJSON(filepath.Join(p.Hardware, "profile.json"), &profile); e != nil {
		return e
	}
	if profile.KernelRelease != release || profile.KernelBuild != 17 || profile.Compatibility != "verified-captured-baseline" {
		return errors.New("hardware bundle does not match the required kernel and compatibility profile")
	}
	evidence := map[string]Entry{}
	for _, v := range hardware.Artifacts {
		evidence[v.Path] = v
	}
	var used []Entry
	verifiedCopy := func(name, dest string) error {
		item, ok := evidence[name]
		if !ok {
			return fmt.Errorf("hardware manifest entry missing: %s", name)
		}
		source, e := safeJoin(p.Hardware, name)
		if e != nil {
			return e
		}
		if e = copyFile(source, dest, 0644); e != nil {
			return e
		}
		rel, e := filepath.Rel(target, dest)
		if e != nil {
			return e
		}
		used = append(used, Entry{Path: filepath.ToSlash(rel), Source: name, Bytes: item.Bytes, SHA256: item.SHA256})
		return nil
	}
	for _, name := range []string{"kernel/running.config", "device-tree/boot-image.dts", "device-tree/boot-image.dtb", "kernel/build-provenance.json"} {
		if e := verifiedCopy(name, filepath.Join(target, "usr/share/microstick/hardware", filepath.Base(name))); e != nil {
			return e
		}
	}
	var fm FirmwareMap
	if e := readJSON(filepath.Join(p.Hardware, "derived/firmware-map.json"), &fm); e != nil {
		return e
	}
	for _, v := range fm.Files {
		if v.Component == "wifi-device-NV" {
			if e := verifiedCopy(v.Artifact, filepath.Join(target, strings.TrimPrefix(v.Target, "/"))); e != nil {
				return e
			}
		}
	}
	var dsp DSP
	if e := readJSON(filepath.Join(p.Hardware, "dsp-firmware.json"), &dsp); e != nil {
		return e
	}
	os.Remove(filepath.Join(target, "usr/share/microstick/default-imei.json"))
	guardPath := filepath.Join(target, "usr/share/microstick/modem-guard.sha256")
	if len(dsp.Guard) > 0 {
		if dsp.Guard["item"] != float64(2500) || dsp.Guard["marker"] != "HiMI_OK" || dsp.Guard["write_only_when_zero"] != true {
			return errors.New("invalid zero-only modem guard")
		}
		match := false
		for _, v := range dsp.Files {
			if v.Name == dsp.Guard["firmware_file"] && v.SHA256 == dsp.Guard["firmware_sha256"] {
				match = true
			}
		}
		if !match {
			return errors.New("guard does not match DSP profile")
		}
		if e := os.WriteFile(guardPath, []byte(fmt.Sprint(dsp.Guard["firmware_sha256"])+"  /lib/firmware/"+fmt.Sprint(dsp.Guard["firmware_file"])+"\n"), 0644); e != nil {
			return e
		}
	} else {
		os.Remove(guardPath)
	}
	for _, v := range dsp.Files {
		path, e := safeJoin(p.Hardware, v.Source)
		if e != nil {
			return e
		}
		h, e := hashFile(path)
		if e != nil {
			return e
		}
		st, e := os.Stat(path)
		if e != nil {
			return e
		}
		if h != v.SHA256 || st.Size() != v.Bytes {
			return errors.New("DSP source checksum mismatch")
		}
	}
	for _, pattern := range []string{"modem.*", "wcnss.*"} {
		names, _ := filepath.Glob(filepath.Join(target, "lib/firmware", pattern))
		for _, name := range names {
			if e := os.Remove(name); e != nil {
				return e
			}
		}
	}
	for _, v := range dsp.Files {
		if e := copyFile(filepath.Join(p.Hardware, v.Source), filepath.Join(target, "lib/firmware", v.Name), 0644); e != nil {
			return e
		}
		used = append(used, Entry{Path: "lib/firmware/" + v.Name, Source: v.Source, Bytes: v.Bytes, SHA256: v.SHA256})
	}
	prefix := "kernel/kernel-and-modules-tree/lib/modules/" + release
	selected, e := ModuleSelection(filepath.Join(p.Hardware, prefix))
	if e != nil {
		return e
	}
	dest := filepath.Join(target, "lib/modules", release)
	if e = os.RemoveAll(dest); e != nil {
		return e
	}
	for _, name := range selected {
		if e = verifiedCopy(prefix+"/"+name, filepath.Join(dest, name)); e != nil {
			return e
		}
	}
	for _, name := range []string{"modules.builtin", "modules.builtin.modinfo", "modules.softdep"} {
		if e = verifiedCopy(prefix+"/"+name, filepath.Join(dest, name)); e != nil {
			return e
		}
	}
	if e = os.WriteFile(filepath.Join(dest, "modules.order"), []byte(strings.Join(selected, "\n")+"\n"), 0644); e != nil {
		return e
	}
	if _, e = command("", nil, "depmod", "-b", target, release); e != nil {
		return e
	}
	if e = verifiedCopy("userspace/integration-tree/lib/udev/rules.d/77-mm-qcom-soc.rules", filepath.Join(target, "etc/udev/rules.d/78-microstick-qcom-soc.rules")); e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(target, "etc/udev/rules.d/79-microstick-nv.rules"), []byte("SUBSYSTEM==\"block\", ENV{DEVTYPE}==\"partition\", ENV{PARTNAME}==\"?*\", SYMLINK+=\"disk/by-partlabel/$env{PARTNAME}\"\n"), 0644); e != nil {
		return e
	}
	for _, name := range []string{"S35iptables", "S40network", "S44modem-manager", "S50dnsmasq", "S80dnsmasq", "S50telnet", "S50crond", "S50httpd", "S90httpd", "S50avahi-daemon"} {
		os.Remove(filepath.Join(target, "etc/init.d", name))
	}
	os.Remove(filepath.Join(target, "usr/share/dbus-1/system-services/org.freedesktop.ModemManager1.service"))
	for _, name := range []string{"usr/sbin/NetworkManager", "usr/bin/nmcli", "sbin/dhclient"} {
		if _, e = os.Stat(filepath.Join(target, name)); e == nil {
			return fmt.Errorf("unexpected conflicting service: %s", name)
		}
	}
	private := filepath.Join(p.Work, "buildroot-private")
	if e = os.MkdirAll(private, 0700); e != nil {
		return e
	}
	if e = os.Chmod(private, 0700); e != nil {
		return e
	}
	credentialPath := filepath.Join(private, "credentials.json")
	var credentials Credentials
	if e = readJSON(credentialPath, &credentials); os.IsNotExist(e) {
		digits := make([]byte, 8)
		for i := range digits {
			n, e := rand.Int(rand.Reader, bigTen)
			if e != nil {
				return e
			}
			digits[i] = byte('0' + n.Int64())
		}
		token := make([]byte, 18)
		if _, e = rand.Read(token); e != nil {
			return e
		}
		credentials = Credentials{"Microstick-" + profile.DeviceID, string(digits), hex.EncodeToString(token), true}
		if e = writeJSON(credentialPath, credentials, 0600); e != nil {
			return e
		}
	} else if e != nil {
		return e
	}
	if !credentials.Branded {
		credentials.SSID = strings.ReplaceAll(credentials.SSID, "Dongle-", "Microstick-")
		credentials.Branded = true
		if e = writeJSON(credentialPath, credentials, 0600); e != nil {
			return e
		}
	}
	saltBytes := make([]byte, 32)
	if _, e = rand.Read(saltBytes); e != nil {
		return e
	}
	salt := hex.EncodeToString(saltBytes)
	auth := map[string]string{"salt": salt, "hash": hex.EncodeToString(pbkdf2([]byte(credentials.WiFiPassword), []byte(salt), 120000, 32, sha256.New))}
	if e = writeJSON(filepath.Join(target, "usr/share/microstick/default-plugins/web/default-auth.json"), auth, 0600); e != nil {
		return e
	}
	for _, name := range []string{"etc/dongle", "usr/lib/dongle", "usr/share/dongle"} {
		if e = os.RemoveAll(filepath.Join(target, name)); e != nil {
			return e
		}
	}
	for _, name := range []string{"usr/sbin/httpd", "usr/sbin/donglectl", "etc/init.d/S02dongle", "etc/init.d/S35dongle"} {
		os.Remove(filepath.Join(target, name))
	}
	old, _ := filepath.Glob(filepath.Join(target, "usr/libexec/dongle-*"))
	for _, name := range old {
		os.Remove(name)
	}
	passwd := filepath.Join(target, "etc/passwd")
	data, e := os.ReadFile(passwd)
	if e != nil {
		return e
	}
	if !strings.Contains(string(data), "microstick-web:") {
		if strings.Contains(string(data), ":405:") {
			return errors.New("web UID405 already used")
		}
		if e = appendText(passwd, "microstick-web:x:405:405:Microstick web:/nonexistent:/bin/false\n"); e != nil {
			return e
		}
		if e = appendText(filepath.Join(target, "etc/group"), "microstick-web:x:405:\n"); e != nil {
			return e
		}
	}
	psk := hex.EncodeToString(pbkdf2([]byte(credentials.WiFiPassword), []byte(credentials.SSID), 4096, 32, sha1.New))
	etc := filepath.Join(target, "etc/microstick")
	if e = os.MkdirAll(etc, 0700); e != nil {
		return e
	}
	hostapd := "interface=wlan0\ndriver=nl80211\nctrl_interface=/run/hostapd\nssid=" + credentials.SSID + "\nhw_mode=g\nchannel=1\nieee80211n=1\nwmm_enabled=1\nauth_algs=1\nwpa=2\nwpa_key_mgmt=WPA-PSK\nrsn_pairwise=CCMP\nwpa_psk=" + psk + "\n"
	if e = os.WriteFile(filepath.Join(etc, "hostapd.conf"), []byte(hostapd), 0600); e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(etc, "build-epoch"), []byte(strconv.FormatInt(epoch, 10)+"\n"), 0600); e != nil {
		return e
	}
	credentialHash, e := hashFile(credentialPath)
	if e != nil {
		return e
	}
	passwordHash, e := command("", []byte(credentials.RootPassword+"\n"), "openssl", "passwd", "-6", "-salt", credentialHash[:16], "-stdin")
	if e != nil {
		return e
	}
	shadow := filepath.Join(target, "etc/shadow")
	data, e = os.ReadFile(shadow)
	if e != nil {
		return e
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "root:") {
			lines[i] = "root:" + strings.TrimSpace(string(passwordHash)) + ":" + strconv.FormatInt(epoch/86400, 10) + ":0:99999:7:::"
		}
	}
	if e = os.WriteFile(shadow, []byte(strings.Join(lines, "\n")+"\n"), 0600); e != nil {
		return e
	}
	if e = os.Chmod(shadow, 0600); e != nil {
		return e
	}
	entries, e := os.ReadDir(etc)
	if e != nil {
		return e
	}
	for _, v := range entries {
		if e = os.Chmod(filepath.Join(etc, v.Name()), 0600); e != nil {
			return e
		}
	}
	os.Remove(filepath.Join(target, "etc/resolv.conf"))
	if e = os.WriteFile(filepath.Join(target, "etc/resolv.conf"), []byte("nameserver 127.0.0.1\n"), 0644); e != nil {
		return e
	}
	for _, name := range []string{"var/lib", "etc/dropbear"} {
		path := filepath.Join(target, name)
		if st, e := os.Lstat(path); e == nil && st.Mode()&os.ModeSymlink != 0 {
			if e = os.Remove(path); e != nil {
				return e
			}
		}
		if e = os.MkdirAll(path, 0700); e != nil {
			return e
		}
	}
	if e = os.MkdirAll(filepath.Join(target, "var/lib/microstick"), 0700); e != nil {
		return e
	}
	info := filepath.Join(target, "usr/share/microstick")
	if e = writeJSON(filepath.Join(info, "hardware-assets.json"), used, 0644); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(info, "build.json"), map[string]any{"buildroot": "2025.02.19", "libc": "musl", "kernel": release, "kernel_build": 17, "source_date_epoch": epoch, "module_count": len(selected), "hardware_tested": false, "device_only": profile.DeviceID, "personalized": false}, 0644); e != nil {
		return e
	}
	rawProviders, e := os.ReadFile(filepath.Join(target, "usr/share/mobile-broadband-provider-info/serviceproviders.xml"))
	if e != nil {
		return e
	}
	reviewedProviders, e := os.ReadFile(filepath.Join(p.External, "board/msm8916/operator-profiles.json"))
	if e != nil {
		return e
	}
	providers, e := providerCatalog(rawProviders, reviewedProviders)
	if e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(info, "providers.json"), providers, 0644); e != nil {
		return e
	}
	fmt.Printf("Installed %d kernel modules; private credentials retained\n", len(selected))
	return nil
}
func appendText(path, text string) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0644)
	if e != nil {
		return e
	}
	defer f.Close()
	_, e = f.WriteString(text)
	return e
}
