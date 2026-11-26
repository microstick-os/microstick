package dev

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OpenWorkspace and ExternalPath share the developer tools' symlink-aware
// boundary checks with the independent flasher command.
func OpenWorkspace(repo string) (Paths, error) { return paths(repo, true) }
func ExternalPath(repo, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("private path must be absolute")
	}
	return privatePath(repo, path)
}
func VerifyImages(root string) error { _, e := verifyManifest(root); return e }
func FlasherBootstrap(p Paths, edl bool) (mapper, bridge, programmer string, err error) {
	return FlasherBootstrapContext(context.Background(), p, edl)
}
func FlasherBootstrapContext(ctx context.Context, p Paths, edl bool) (mapper, bridge, programmer string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	if err = ctx.Err(); err != nil {
		return
	}
	command := func(directory string, input []byte, name string, args ...string) ([]byte, error) {
		return commandContext(ctx, directory, input, name, args...)
	}
	fetch := func(path, url, hash string) error { return FetchContext(ctx, path, url, hash) }
	if p.Distribution != "" {
		mapper = filepath.Join(p.Distribution, "tools/bin/extmap")
		if edl {
			bridge = filepath.Join(p.Distribution, "tools/bin/edl-read")
			programmer = filepath.Join(p.Distribution, "resources/programmer.mbn")
		}
		return
	}
	dir := filepath.Join(p.Work, "host-tools/microstick-flasher")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return
	}
	mapper = filepath.Join(dir, "extmap")
	flags, e := command("", nil, "pkg-config", "--cflags", "--libs", "ext2fs")
	if e != nil {
		err = e
		return
	}
	args := []string{"-O2", "-Wall", "-Wextra", "-Werror", "-D_FILE_OFFSET_BITS=64", filepath.Join(p.Repo, "scripts/native/extmap.c"), "-o", mapper}
	args = append(args, strings.Fields(string(flags))...)
	if _, err = command("", nil, "cc", args...); err != nil {
		return
	}
	if !edl {
		return
	}
	archive := filepath.Join(p.Work, "buildroot-downloads/qdl-"+qdlRevision+".tar.gz")
	if err = fetch(archive, "https://codeload.github.com/linux-msm/qdl/tar.gz/"+qdlRevision, "e0110b6f667cc937baa0a333e8fb0187d010db7dd78ba68092223d8c4397c21d"); err != nil {
		return
	}
	stage, e := os.MkdirTemp(dir, ".qdl-build-")
	if e != nil {
		err = e
		return
	}
	defer os.RemoveAll(stage)
	if _, err = command("", nil, "tar", "-xf", archive, "-C", stage); err != nil {
		return
	}
	source := filepath.Join(stage, "qdl-"+qdlRevision)
	if err = patchFlasherQDL(p, source); err != nil {
		return
	}
	flags, e = command("", nil, "pkg-config", "--cflags", "--libs", "libusb-1.0", "libxml-2.0")
	if e != nil {
		err = e
		return
	}
	bridge = filepath.Join(dir, "edl-read")
	args = []string{"-O2", "-D_FILE_OFFSET_BITS=64", "-I" + filepath.Join(p.Repo, "scripts/native"), "-I" + filepath.Join(source, "include"), "-I" + filepath.Join(source, "src"), "-I" + source, filepath.Join(p.Repo, "scripts/native/edl-read.c")}
	for _, name := range []string{"sahara", "util", "ux", "oscompat", "file", "file_nozip", "auto", "cli", "qud", "firehose", "input_type", "io", "patch", "program", "programmer", "read", "sahara_config", "sha2", "sim", "ufs", "usb", "vip", "sparse", "gpt", "flashmap", "json", "contents", "pathbuf"} {
		args = append(args, filepath.Join(source, "src", name+".c"))
	}
	args = append(args, "-o", bridge)
	args = append(args, strings.Fields(string(flags))...)
	if _, err = command("", nil, "cc", args...); err != nil {
		return
	}
	programmer = filepath.Join(p.Work, "buildroot-downloads/prog_emmc_firehose_8916.mbn")
	err = fetch(programmer, "https://raw.githubusercontent.com/bkerler/Loaders/6c416255056a178f54ccd7ee9b67d04a5bdfa8e6/qualcomm/factory/msm8916/007050e100000000_394a2e47cf830150_fhprg_peek.bin", programmerSHA)
	return
}

func patchFlasherQDL(p Paths, source string) (err error) {
	replaceSection := func(start, end, include string) error {
		path := filepath.Join(source, "src/firehose.c")
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		i := bytes.Index(b, []byte(start))
		j := bytes.Index(b, []byte(end))
		if i < 0 || j <= i {
			return errors.New("pinned QDL section mismatch")
		}
		b = append(append(append([]byte{}, b[:i]...), []byte("#include \""+include+"\"\n\n")...), b[j:]...)
		return os.WriteFile(path, b, 0600)
	}
	if err = replaceSection("static int firehose_read(struct", "static int firehose_vip_send_table(", "edl-stream.h"); err != nil {
		return
	}
	if err = replaceSection("struct firehose_getsize_args {", "static int firehose_getstorageinfo(struct", "edl-storage.h"); err != nil {
		return
	}
	patch := func(path, old, replacement string) error {
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Count(b, []byte(old)) != 1 {
			return errors.New("pinned QDL patch mismatch")
		}
		return os.WriteFile(path, bytes.Replace(b, []byte(old), []byte(replacement), 1), 0600)
	}
	if err = patch(filepath.Join(source, "src/firehose.c"), "ret = firehose_getstorageinfo(qdl, lun, &args);", "microstick_response_ready = microstick_geometry_ready;\n\tret = firehose_getstorageinfo(qdl, lun, &args);\n\tmicrostick_response_ready = NULL;"); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/firehose.c"), "firehose_read(qdl, 30000, firehose_getstorageinfo_parser, args)", "firehose_read(qdl, 3000, firehose_getstorageinfo_parser, args)"); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/usb.c"), "qdl->max_payload_size = 1048576;", "qdl->max_payload_size = 16384;"); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/usb.c"), "static bool usb_match_serial(", "#include \"edl-usb.h\"\nstatic bool usb_match_serial("); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/usb.c"), "/* If no serial is requested, consider everything a match */", "if (serial && serial[0]=='@') { char path[64]; if(microstick_usb_path(libusb_get_device(handle),path,sizeof(path))) return false; return !strcmp(serial,path); }\n\t/* If no serial is requested, consider everything a match */"); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/sahara.c"), "static int sahara_command_info(", "extern void microstick_identity(uint32_t, uint64_t);\nstatic int sahara_command_info("); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/sahara.c"), "ux_info(\"Sahara protocol version: %u\\n\", version);", "if (!have_serial || !serial || !have_hwid || !hwid) return -1;\n\tmicrostick_identity(serial, hwid);\n\tux_info(\"Sahara protocol version: %u\\n\", version);"); err != nil {
		return
	}
	// Stream one exact program operation in 16KiB chunks instead of allocating
	// an entire rootfs in C. This function is unreachable before a plan grant.
	if err = patch(filepath.Join(source, "src/firehose.c"), "ssize_t firehose_pwrite(", "extern int microstick_next_write(void *, size_t);\nssize_t firehose_pwrite("); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/firehose.c"), "const uint8_t *p = buf;", "const uint8_t *p = buf;\n\tuint8_t streamed[16384];"); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/firehose.c"), "ret = qdl_write(qdl, p, chunk * sector_size, 10000);", "if (!buf) { if (chunk * sector_size > sizeof(streamed) || microstick_next_write(streamed, chunk * sector_size)) return -1; p = streamed; }\n\t\tret = qdl_write(qdl, p, chunk * sector_size, 10000);"); err != nil {
		return
	}
	if err = patch(filepath.Join(source, "src/firehose.c"), "p += chunk * sector_size;", "if (buf) p += chunk * sector_size;"); err != nil {
		return
	}
	if err = os.WriteFile(filepath.Join(source, "version.h"), []byte("#define VERSION \"microstick-read-session-"+qdlRevision+"\"\n"), 0600); err != nil {
		return
	}
	return
}
