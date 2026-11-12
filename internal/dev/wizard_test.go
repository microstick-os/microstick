package dev

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testExt4(t *testing.T, dir, name string) string {
	t.Helper()
	image := filepath.Join(dir, name)
	f, e := os.Create(image)
	if e != nil {
		t.Fatal(e)
	}
	f.Truncate(16 << 20)
	f.Close()
	if _, e = command("", nil, "mke2fs", "-q", "-F", "-t", "ext4", image); e != nil {
		t.Fatal(e)
	}
	return image
}
func TestMigrationPreservesDurableTreesAndMetadata(t *testing.T) {
	dir := t.TempDir()
	src := testExt4(t, dir, "src.ext4")
	dst := testExt4(t, dir, "dst.ext4")
	local := filepath.Join(dir, "file")
	mustWrite(t, local, "persisted settings and plugin history\n")
	for _, image := range []string{src, dst} {
		for _, p := range []string{"/etc", "/data", "/var", "/var/lib", "/root"} {
			q, _ := dq(p)
			if _, e := debugImage(image, "mkdir "+q, true); e != nil {
				t.Fatal(e)
			}
		}
		mustWrite(t, local, "root:x:0:0::/root:/bin/sh\nmicrostick-web:x:405:405::/:/bin/false\n")
		if e := imagePut(image, "/etc/passwd", local, 0100644); e != nil {
			t.Fatal(e)
		}
	}
	for _, p := range []string{"/etc/microstick", "/data/plugins", "/data/plugins/demo", "/data/plugins/demo/.state", "/var/lib/microstick", "/etc/dropbear", "/root/.ssh"} {
		q, _ := dq(p)
		if _, e := debugImage(src, "mkdir "+q, true); e != nil {
			t.Fatal(e)
		}
	}
	mustWrite(t, local, "persisted settings and plugin history\n")
	for _, p := range []string{"/etc/microstick/network.conf", "/data/plugins/demo/.state/history", "/var/lib/microstick/state", "/etc/dropbear/hostkey", "/root/.ssh/authorized_keys"} {
		if e := imagePut(src, p, local, 0100600); e != nil {
			t.Fatal(e)
		}
	}
	for _, cmd := range []string{"set_inode_field /data/plugins/demo/.state/history uid 405", "set_inode_field /data/plugins/demo/.state/history gid 405", "set_inode_field /data/plugins/demo/.state/history mtime @1600000000", "ea_set /data/plugins/demo/.state/history user.example retained", "symlink /data/plugins/demo/link .state/history", "ln /data/plugins/demo/.state/history /data/plugins/demo/hardlink", "set_inode_field /data/plugins/demo/.state/history links_count 2"} {
		if _, e := debugImage(src, cmd, true); e != nil {
			t.Fatal(e)
		}
	}
	before, e := hashFile(src)
	if e != nil {
		t.Fatal(e)
	}
	if e = MigrateUserData(src, dst, dir); e != nil {
		t.Fatal(e)
	}
	after, _ := hashFile(src)
	if before != after {
		t.Fatal("migration modified source")
	}
	got, e := imageBytes(dst, "/data/plugins/demo/.state/history")
	if e != nil || string(got) != "persisted settings and plugin history\n" {
		t.Fatal("history lost", e)
	}
	a, e := imageStat(dst, "/data/plugins/demo/.state/history")
	if e != nil {
		t.Fatal(e)
	}
	b, e := imageStat(dst, "/data/plugins/demo/hardlink")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(inodeRE.FindSubmatch(a)[1], inodeRE.FindSubmatch(b)[1]) {
		t.Fatal("hardlinks lost")
	}
	if !bytes.Contains(a, []byte("405")) || !bytes.Contains(a, []byte("2020")) {
		t.Fatalf("metadata lost: %s", a)
	}
	attrs, e := debugImage(dst, "ea_list /data/plugins/demo/.state/history", false)
	if e != nil || !bytes.Contains(attrs, []byte("user.example")) {
		t.Fatal("xattr lost", e)
	}
	link, e := imageStat(dst, "/data/plugins/demo/link")
	if e != nil || !bytes.Contains(link, []byte(".state/history")) {
		t.Fatal("symlink lost", e)
	}
}
func TestIMEICollisionReservation(t *testing.T) {
	first, _ := FormatIMEI(DefaultTAC, 1)
	second, _ := FormatIMEI(DefaultTAC, 2)
	calls := 0
	value, e := uniqueIMEI(DefaultTAC, map[string]bool{first: true}, func(string) (string, error) {
		calls++
		if calls == 1 {
			return first, nil
		}
		return second, nil
	})
	if e != nil || value != second || calls != 2 {
		t.Fatal("collision not retried", e)
	}
	if _, e = uniqueIMEI(DefaultTAC, map[string]bool{first: true}, func(string) (string, error) { return first, nil }); e == nil {
		t.Fatal("exhausted namespace accepted")
	}
}
func TestRealWizardPreparationRetryAndSecrets(t *testing.T) {
	p := testPaths(t)
	p.Work = t.TempDir()
	dir := t.TempDir()
	baseBoot, e := hashFile(filepath.Join(p.Artifacts, "boot.img"))
	if e != nil {
		t.Fatal(e)
	}
	baseRoot, e := hashFile(filepath.Join(p.Artifacts, "rootfs.ext4"))
	if e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(dir, "output")
	state := filepath.Join(dir, "install.json")
	o := WizardOptions{SSID: "Fixture-AP", APPassword: "fixture-ap-secret", RootPassword: "fixture-root-secret", RootPARTUUID: "01234567-89ab-cdef-0123-456789abcdef", Clean: true}
	if e = PrepareWizard(p, p.Artifacts, state, output, o); e != nil {
		t.Fatal(e)
	}
	m, e := verifyManifest(output)
	if e != nil || !m.Personalized {
		t.Fatal("invalid prepared bundle", e)
	}
	image := filepath.Join(output, "rootfs.ext4")
	ap, e := imageBytes(image, "/etc/microstick/hostapd.conf")
	if e != nil {
		t.Fatal(e)
	}
	expected := hex.EncodeToString(pbkdf2([]byte(o.APPassword), []byte(o.SSID), 4096, 32, sha1.New))
	if !bytes.Contains(ap, []byte(expected)) || bytes.Contains(ap, []byte(o.APPassword)) {
		t.Fatal("AP derivation wrong or plaintext stored")
	}
	shadow, e := imageBytes(image, "/etc/shadow")
	if e != nil || bytes.Contains(shadow, []byte(o.RootPassword)) {
		t.Fatal("root credential leaked", e)
	}
	for _, line := range strings.Split(string(shadow), "\n") {
		if strings.HasPrefix(line, "root:") {
			fields := strings.Split(line, ":")
			parts := strings.Split(fields[1], "$")
			if len(parts) < 4 {
				t.Fatal("missing root password hash")
			}
			derived, e := command("", []byte(o.RootPassword+"\n"), "openssl", "passwd", "-6", "-salt", parts[2], "-stdin")
			if e != nil || strings.TrimSpace(string(derived)) != fields[1] {
				t.Fatal("selected root password does not match stored hash")
			}
		}
	}
	authBytes, e := imageBytes(image, "/usr/share/microstick/default-plugins/web/default-auth.json")
	if e != nil {
		t.Fatal(e)
	}
	var auth map[string]string
	if e = json.Unmarshal(authBytes, &auth); e != nil {
		t.Fatal(e)
	}
	if auth["hash"] != hex.EncodeToString(pbkdf2([]byte(o.APPassword), []byte(auth["salt"]), 120000, 32, sha256.New)) {
		t.Fatal("initial panel password differs from AP")
	}
	boot, e := os.ReadFile(filepath.Join(output, "boot.img"))
	if e != nil || !bytes.Contains(boot, []byte(o.RootPARTUUID)) {
		t.Fatal("actual PARTUUID missing", e)
	}
	beforeBoot, _ := hashFile(filepath.Join(output, "boot.img"))
	beforeRoot, _ := hashFile(image)
	if e = PrepareWizard(p, p.Artifacts, state, output, o); e != nil {
		t.Fatal(e)
	}
	afterBoot, _ := hashFile(filepath.Join(output, "boot.img"))
	afterRoot, _ := hashFile(image)
	if beforeBoot != afterBoot || beforeRoot != afterRoot {
		t.Fatal("retry changed bytes")
	}
	o.APPassword = "changed-fixture-secret"
	if e = PrepareWizard(p, p.Artifacts, state, output, o); e == nil {
		t.Fatal("changed retry accepted")
	}
	nowBoot, _ := hashFile(filepath.Join(p.Artifacts, "boot.img"))
	nowRoot, _ := hashFile(filepath.Join(p.Artifacts, "rootfs.ext4"))
	if nowBoot != baseBoot || nowRoot != baseRoot {
		t.Fatal("base mutated")
	}
	for _, name := range []string{state, state + ".wizard.json", filepath.Join(output, "installation.json")} {
		b, e := os.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(string(b), "fixture-root-secret") || strings.Contains(string(b), "fixture-ap-secret") {
			t.Fatal("plaintext secret in record")
		}
	}
}
