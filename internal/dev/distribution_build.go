package dev

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"microstick.dev/tools/internal/flasher"
)

func DistributionCLI(p Paths, args []string) error {
	f := flag.NewFlagSet("package-release", flag.ContinueOnError)
	version := f.String("version", "", "release version, for example 1.0.0")
	output := f.String("output", "", "new external release directory")
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 || !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(*version) || *output == "" {
		return errors.New("use package-release --version X.Y.Z --output /absolute/new/directory")
	}
	return PackageDistribution(p, *version, *output)
}

// Package a release from verified inputs without opening a USB device.
func PackageDistribution(p Paths, version, output string) error {
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		return errors.New("invalid release version")
	}
	if e := flasher.CheckEngineReleaseVersion(version); e != nil {
		return e
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return errors.New("release packaging requires Linux x86_64")
	}
	output, e := ExternalPath(p.Repo, output)
	if e != nil {
		return e
	}
	if _, e = os.Lstat(output); !os.IsNotExist(e) {
		return errors.New("select a new release directory")
	}
	if _, e = verifyManifest(p.Artifacts); e != nil {
		return e
	}
	var hardware Manifest
	if e = readJSON(filepath.Join(p.Hardware, "MANIFEST.json"), &hardware); e != nil {
		return e
	}
	if e = verifyEntries(p.Hardware, hardware.Artifacts); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(output), 0755); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(filepath.Dir(output), ".release-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	name := "microstick-" + version + "-linux-x86_64"
	root := filepath.Join(stage, name)
	if e = os.MkdirAll(root, 0755); e != nil {
		return e
	}
	copy := func(src, rel string, mode os.FileMode) error { return copyFile(src, filepath.Join(root, rel), mode) }
	for _, file := range []string{"README.md", "README.ru.md", "docs/development.md", "docs/flasher.md", "docs/architecture.md", "docs/hardware-provenance.json", "boards/resources.json", "buildroot-external/board/msm8916/overlay/usr/share/microstick/default-plugins/README.md", "buildroot-external/board/msm8916/overlay/usr/share/microstick/default-plugins/statistics/README.md", "buildroot-external/board/msm8916/overlay/usr/share/microstick/default-plugins/web/www/favicon.svg"} {
		if e = copy(filepath.Join(p.Repo, file), file, 0644); e != nil {
			return e
		}
	}
	for _, image := range []string{"desktop_en.png", "desktop_ru.png"} {
		file := filepath.Join("docs/images", image)
		if e = copy(filepath.Join(p.Repo, file), file, 0644); e != nil {
			return e
		}
	}
	boardFiles, e := files(filepath.Join(p.Repo, "boards"))
	if e != nil {
		return e
	}
	for _, name := range boardFiles {
		if filepath.Ext(name) == ".json" {
			if e = copy(filepath.Join(p.Repo, "boards", name), filepath.Join("boards", name), 0644); e != nil {
				return e
			}
		}
	}
	if e = publicHardware(p, filepath.Join(root, "hardware")); e != nil {
		return e
	}
	if e = publicImages(p, filepath.Join(root, "images"), version); e != nil {
		return e
	}
	mapper, bridge, programmer, e := FlasherBootstrap(p, true)
	if e != nil {
		return e
	}
	if e = copy(programmer, "resources/programmer.mbn", 0644); e != nil {
		return e
	}
	lk, e := WizardLK2nd(p)
	if e != nil {
		return e
	}
	if e = copy(lk, "resources/lk2nd.img", 0644); e != nil {
		return e
	}
	fw := &flasher.BootFirmware{Policy: flasher.BootFirmwarePolicy, Upgrade: true, Security: "disabled"}
	fw.TZ.Range.Length, fw.HYP.Range.Length = 2<<20, 2<<20
	if e = stageBootFirmware(p, filepath.Join(root, "resources"), fw); e != nil {
		return e
	}
	for tool, src := range map[string]string{"extmap": mapper, "edl-read": bridge, "debugfs": "", "e2fsck": "", "resize2fs": "", "mcopy": "", "mtype": "", "openssl": ""} {
		if src == "" {
			src, e = exec.LookPath(tool)
			if e != nil {
				return e
			}
		}
		if e = packageNative(root, tool, src); e != nil {
			return e
		}
	}
	if e = os.MkdirAll(filepath.Join(root, "libexec"), 0755); e != nil {
		return e
	}
	if _, e = command(p.Repo, nil, "go", "build", "-trimpath", "-ldflags=-s -w -X main.version="+version, "-o", filepath.Join(root, "libexec/microstick-flasher"), "./cmd/microstick-flasher"); e != nil {
		return e
	}
	launcher := `#!/bin/sh
set -eu
bundle=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case "$(uname -sm)" in 'Linux x86_64') ;; *) echo 'This release requires Linux x86_64.' >&2; exit 1;; esac
export MICROSTICK_WORKSPACE="${MICROSTICK_WORKSPACE:-${XDG_DATA_HOME:-$HOME/.local/share}/microstick}"
umask 077
export PATH="$bundle/tools/bin:$PATH"
export OPENSSL_CONF=/dev/null
export MTOOLSRC=/dev/null
exec "$bundle/libexec/microstick-flasher" --distribution "$bundle" "$@"
`
	if e = os.WriteFile(filepath.Join(root, "flasher"), []byte(launcher), 0755); e != nil {
		return e
	}
	if e = packageNotices(p, root); e != nil {
		return e
	}
	names, e := files(root)
	if e != nil {
		return e
	}
	d := Distribution{Version: version, Platform: "linux/amd64"}
	for _, file := range names {
		v, e := entry(root, file)
		if e != nil {
			return e
		}
		d.Files = append(d.Files, v)
	}
	if e = writeJSON(filepath.Join(root, "DISTRIBUTION.json"), d, 0644); e != nil {
		return e
	}
	if e = os.Chmod(stage, 0755); e != nil {
		return e
	}
	if e = archiveDistribution(root, filepath.Join(stage, name+".tar.gz")); e != nil {
		return e
	}
	archive, e := entry(stage, name+".tar.gz")
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(stage, "SHA256SUMS"), []byte(archive.SHA256+"  "+archive.Path+"\n"), 0644); e != nil {
		return e
	}
	if e = os.RemoveAll(root); e != nil {
		return e
	}
	if e = os.Rename(stage, output); e != nil {
		return e
	}
	fmt.Println("PASS: release prepared without device access:", output)
	return nil
}

func publicHardware(p Paths, root string) error {
	copy := func(rel string) error {
		return copyFile(filepath.Join(p.Hardware, rel), filepath.Join(root, rel), 0644)
	}
	for _, rel := range []string{"boot/kernel.gz", "device-tree/boot-image.dtb", "firmware/dsp/mba.mbn"} {
		if e := copy(rel); e != nil {
			return e
		}
	}
	prefix := "kernel/kernel-and-modules-tree/lib/modules/" + release
	names, e := ModuleSelection(filepath.Join(p.Hardware, prefix))
	if e != nil {
		return e
	}
	for _, rel := range append(names, "modules.dep") {
		if e = copy(prefix + "/" + rel); e != nil {
			return e
		}
	}
	var profile Profile
	if e = readJSON(filepath.Join(p.Hardware, "profile.json"), &profile); e != nil {
		return e
	}
	profile.DeviceID = "generic"
	if e = writeJSON(filepath.Join(root, "profile.json"), profile, 0644); e != nil {
		return e
	}
	var original DSP
	if e = readJSON(filepath.Join(p.Hardware, "dsp-firmware.json"), &original); e != nil {
		return e
	}
	dsp := DSP{Guard: map[string]any{}}
	for _, v := range original.Files {
		if v.Name == "mba.mbn" {
			dsp.Files = append(dsp.Files, v)
		}
	}
	if len(dsp.Files) != 1 || dsp.Files[0].SHA256 != "bcd6100886b10d0d36ce17b1ae70fdc4fd429ab3a0337e0b7cef9aa9b04b60c4" {
		return errors.New("verified runtime MBA missing")
	}
	for _, key := range []string{"item", "marker", "write_only_when_zero", "firmware_file", "firmware_sha256"} {
		if v, ok := original.Guard[key]; ok {
			dsp.Guard[key] = v
		}
	}
	if e = writeJSON(filepath.Join(root, "dsp-firmware.json"), dsp, 0644); e != nil {
		return e
	}
	calibration := "firmware/wifi/WCNSS_qcom_wlan_nv.bin"
	if e = os.MkdirAll(filepath.Join(root, filepath.Dir(calibration)), 0755); e != nil {
		return e
	}
	mapping := map[string]any{"files": []any{map[string]any{"artifact_path": calibration, "target_path": "/lib/firmware/wlan/prima/WCNSS_qcom_wlan_nv.bin", "component": "wifi-device-NV", "device_specific": true}}}
	if e = writeJSON(filepath.Join(root, "derived/firmware-map.json"), mapping, 0644); e != nil {
		return e
	}
	return finalize(root, Manifest{Version: 2})
}

func publicImages(p Paths, root, version string) error {
	if e := os.MkdirAll(root, 0755); e != nil {
		return e
	}
	stage, e := os.MkdirTemp(filepath.Dir(root), ".public-rootfs-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	cleanTar := filepath.Join(stage, "rootfs.tar")
	if e = publicRootfsTar(p, filepath.Join(p.Artifacts, "rootfs.tar"), cleanTar); e != nil {
		return e
	}
	image := filepath.Join(root, "rootfs.ext4")
	if _, e = command("", nil, "mke2fs", "-q", "-F", "-t", "ext4", "-b", "4096", "-I", "256", "-m", "0", "-E", "lazy_itable_init=0,lazy_journal_init=0", "-O", "^metadata_csum_seed,^orphan_file", "-d", cleanTar, image, "65536"); e != nil {
		return e
	}
	if _, e = command("", nil, "e2fsck", "-fn", image); e != nil {
		return e
	}
	// Keep local staging space for the target's DSP and calibration. Preparation
	// shrinks the personalized filesystem before creating the transmitted payload.
	boot, e := os.ReadFile(filepath.Join(p.Artifacts, "boot.img"))
	if e != nil {
		return e
	}
	if e = verifyWizardKernel(p, boot); e != nil {
		return e
	}
	boot, e = personalizedBoot(boot, "00000000-0000-0000-0000-000000000000", nil)
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(root, "boot.img"), boot, 0644); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(root, "release.json"), FlasherRelease{Version: version, Published: time.Now().UTC().Format(time.RFC3339Nano)}, 0644); e != nil {
		return e
	}
	return finalize(root, Manifest{Version: 2})
}

func packageNative(root, name, source string) error {
	if e := copyFile(source, filepath.Join(root, "tools/exec", name), 0755); e != nil {
		return e
	}
	deps, e := command("", nil, "ldd", source)
	if e != nil {
		return e
	}
	for _, line := range strings.Split(string(deps), "\n") {
		for _, field := range strings.Fields(line) {
			if filepath.IsAbs(field) {
				if e = copyFile(field, filepath.Join(root, "tools/lib", filepath.Base(field)), 0755); e != nil {
					return e
				}
				break
			}
		}
	}
	wrapper := "#!/bin/sh\nset -eu\ntools=$(CDPATH= cd -- \"$(dirname -- \"$0\")/..\" && pwd)\nexec \"$tools/lib/ld-linux-x86-64.so.2\" --library-path \"$tools/lib\" \"$tools/exec/" + name + "\" \"$@\"\n"
	path := filepath.Join(root, "tools/bin", name)
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	return os.WriteFile(path, []byte(wrapper), 0755)
}

func archiveDistribution(root, destination string) error {
	f, e := os.Create(destination)
	if e != nil {
		return e
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	e = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		h, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(filepath.Dir(root), path)
		if err != nil {
			return err
		}
		h.Name, h.Uid, h.Gid, h.Uname, h.Gname = filepath.ToSlash(rel), 0, 0, "root", "root"
		if err = tw.WriteHeader(h); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		r, err := os.Open(path)
		if err != nil {
			return err
		}
		defer r.Close()
		_, err = io.Copy(tw, r)
		return err
	})
	if e != nil {
		return e
	}
	if e = tw.Close(); e != nil {
		return e
	}
	return gz.Close()
}

func packageNotices(p Paths, root string) error {
	text := "# Component notices\n\nKeep the component license files with this release.\nmicrostick source: https://github.com/microstick-os/microstick.\nThe firmware uses Linux, Buildroot and the packages listed in packages.json.\nHost tools use e2fsprogs, mtools, OpenSSL, QDL and their shared libraries.\nResource URLs and hashes are in boards/resources.json.\nThe boot loader is lk2nd 23.1. Its source is https://github.com/msm8916-mainline/lk2nd.\nQDL source is https://github.com/linux-msm/qdl.\n\nThis directory does not assign a new license to microstick or its components.\n"
	if e := os.WriteFile(filepath.Join(root, "THIRD_PARTY_NOTICES.md"), []byte(text), 0644); e != nil {
		return e
	}
	var packages map[string]map[string]any
	if e := readJSON(filepath.Join(p.Artifacts, "packages.json"), &packages); e != nil {
		return e
	}
	public := map[string]map[string]any{}
	for name, metadata := range packages {
		public[name] = map[string]any{}
		for _, key := range []string{"name", "version", "licenses", "redistributable"} {
			if value, ok := metadata[key]; ok {
				public[name][key] = value
			}
		}
		var sources []string
		if downloads, ok := metadata["downloads"].([]any); ok {
			for _, download := range downloads {
				if item, ok := download.(map[string]any); ok {
					if uris, ok := item["uris"].([]any); ok {
						for _, uri := range uris {
							if text, ok := uri.(string); ok {
								v, err := url.Parse(text)
								if err == nil && v.Host != "" && (v.Scheme == "https" || v.Scheme == "http" || v.Scheme == "ftp") {
									sources = append(sources, text)
								}
							}
						}
					}
				}
			}
		}
		public[name]["sources"] = sources
	}
	if e := writeJSON(filepath.Join(root, "packages.json"), public, 0644); e != nil {
		return e
	}
	for _, dir := range []string{"spdx", "gcc", "icu", "libxml2", "openssl", "systemd", "zlib-ng", "zlib-ng-compat"} {
		src := filepath.Join("/usr/share/licenses", dir)
		if _, e := os.Stat(src); e == nil {
			if e = copyLicenseTree(src, filepath.Join(root, "licenses/host", dir)); e != nil {
				return e
			}
		}
	}
	dirs, e := os.ReadDir(filepath.Join(p.Output, "build"))
	if e != nil {
		return e
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		for _, pattern := range []string{"COPYING*", "LICENSE*", "LICENCE*", "COPYRIGHT*"} {
			names, _ := filepath.Glob(filepath.Join(p.Output, "build", dir.Name(), pattern))
			for _, name := range names {
				st, e := os.Stat(name)
				if e == nil && st.Mode().IsRegular() {
					if e = copyFile(name, filepath.Join(root, "licenses/target", dir.Name(), filepath.Base(name)), 0644); e != nil {
						return e
					}
				}
			}
		}
	}
	return nil
}

func copyLicenseTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, path)
		if e != nil {
			return e
		}
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(destination, rel), 0755)
		}
		return copyFile(path, filepath.Join(destination, rel), 0644)
	})
}
