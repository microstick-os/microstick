package dev

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const DefaultTAC = "35290611"
const defaultIMEIPath = "/usr/share/microstick/default-imei.json"

var asciiTAC = regexp.MustCompile(`^[0-9]{8}$`)
var asciiIMEI = regexp.MustCompile(`^[0-9]{15}$`)

func luhnSum(text string) int {
	total := 0
	for i, c := range text {
		n := int(c - '0')
		if i%2 == 1 {
			n *= 2
		}
		total += n/10 + n%10
	}
	return total
}
func ValidIMEI(text string) bool {
	return asciiIMEI.MatchString(text) && text != "000000000000000" && luhnSum(text)%10 == 0
}
func FormatIMEI(tac string, serial uint32) (string, error) {
	if !asciiTAC.MatchString(tac) || tac == "00000000" || serial >= 1000000 {
		return "", errors.New("TAC must be eight ASCII digits (not all zero); serial must fit six digits")
	}
	body := fmt.Sprintf("%s%06d", tac, serial)
	return body + fmt.Sprint((10-luhnSum(body)%10)%10), nil
}
func GenerateIMEI(tac string) (string, error) {
	n, e := rand.Int(rand.Reader, big.NewInt(1000000))
	if e != nil {
		return "", e
	}
	return FormatIMEI(tac, uint32(n.Uint64()))
}

type Installation struct {
	Version int               `json:"schema_version"`
	ID      string            `json:"installation_id"`
	Created string            `json:"created_at"`
	TAC     string            `json:"tac"`
	IMEI    string            `json:"imei"`
	Base    map[string]string `json:"base"`
}

func verifyManifest(root string) (Manifest, error) {
	var m Manifest
	if e := readJSON(filepath.Join(root, "MANIFEST.json"), &m); e != nil {
		return m, e
	}
	if e := verifyEntries(root, m.Artifacts); e != nil {
		return m, e
	}
	seen := map[string]bool{}
	for _, v := range m.Artifacts {
		seen[v.Path] = true
	}
	if !seen["boot.img"] || !seen["rootfs.ext4"] {
		return m, errors.New("boot/rootfs images missing from manifest")
	}
	return m, nil
}
func imageDefault(image string) (string, error) {
	out, e := command("", nil, "debugfs", "-R", "cat "+defaultIMEIPath, image)
	if e != nil {
		return "", e
	}
	if strings.TrimSpace(string(out)) == "" {
		return "", nil
	}
	var value struct {
		IMEI string `json:"imei"`
	}
	if e = json.Unmarshal(out, &value); e != nil {
		return "", e
	}
	return value.IMEI, nil
}
func injectDefault(image, dir, value string) error {
	data, _ := json.Marshal(map[string]string{"imei": value})
	if e := os.WriteFile(filepath.Join(dir, "default-imei.json"), append(data, '\n'), 0600); e != nil {
		return e
	}
	requests := "write default-imei.json " + defaultIMEIPath + "\nset_inode_field " + defaultIMEIPath + " mode 0100644\nset_inode_field " + defaultIMEIPath + " uid 0\nset_inode_field " + defaultIMEIPath + " gid 0\n"
	if e := os.WriteFile(filepath.Join(dir, "debugfs.commands"), []byte(requests), 0600); e != nil {
		return e
	}
	if _, e := command(dir, nil, "debugfs", "-w", "-f", "debugfs.commands", image); e != nil {
		return e
	}
	actual, e := imageDefault(image)
	if e != nil {
		return e
	}
	if actual != value {
		return errors.New("default IMEI readback mismatch")
	}
	stat, e := command("", nil, "debugfs", "-R", "stat "+defaultIMEIPath, image)
	if e != nil {
		return e
	}
	if !regexp.MustCompile(`User:\s+0\s+Group:\s+0`).Match(stat) || !regexp.MustCompile(`Mode:\s+0644\b`).Match(stat) {
		return errors.New("default IMEI owner/mode mismatch")
	}
	os.Remove(filepath.Join(dir, "default-imei.json"))
	return os.Remove(filepath.Join(dir, "debugfs.commands"))
}
func privatePath(repo, path string) (string, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	if within(abs, repo) {
		return "", errors.New("private files must be outside the source repository")
	}
	if st, e := os.Lstat(abs); e == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("private path cannot be a symlink")
	}
	parent := filepath.Dir(abs)
	missing := []string{filepath.Base(abs)}
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			abs = resolved
			for i := len(missing) - 1; i >= 0; i-- {
				abs = filepath.Join(abs, missing[i])
			}
			break
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		missing = append(missing, filepath.Base(parent))
		parent = next
	}
	if within(abs, repo) {
		return "", errors.New("private path resolves inside source repository")
	}
	return abs, nil
}
func Prepare(p Paths, images, statePath, output, tac string, chosenID ...string) error {
	images, e := filepath.Abs(images)
	if e != nil {
		return e
	}
	statePath, e = privatePath(p.Repo, statePath)
	if e != nil {
		return e
	}
	output, e = privatePath(p.Repo, output)
	if e != nil {
		return e
	}
	if within(output, images) || within(images, output) || within(statePath, output) || within(statePath, images) {
		return errors.New("input images, installation state and output must be separate")
	}
	baseManifest, e := verifyManifest(images)
	if e != nil {
		return e
	}
	old, e := imageDefault(filepath.Join(images, "rootfs.ext4"))
	if e != nil {
		return e
	}
	if baseManifest.Personalized || old != "" {
		return errors.New("use a verified unpersonalized base")
	}
	base := map[string]string{}
	for _, n := range []string{"boot.img", "rootfs.ext4"} {
		base[n], e = hashFile(filepath.Join(images, n))
		if e != nil {
			return e
		}
	}
	if tac != "" && (!asciiTAC.MatchString(tac) || tac == "00000000") {
		return errors.New("invalid TAC")
	}
	if e = os.MkdirAll(filepath.Dir(statePath), 0700); e != nil {
		return e
	}
	// All local records share one lock, so concurrent new installations cannot
	// both reserve the same randomly generated identity.
	registry := filepath.Dir(statePath)
	if p.Work != "" {
		registry = filepath.Join(p.Work, "buildroot-private/installations")
	}
	if e = os.MkdirAll(registry, 0700); e != nil {
		return e
	}
	registryFD, e := syscall.Open(filepath.Join(registry, ".registry.lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer syscall.Close(registryFD)
	if e = syscall.Flock(registryFD, syscall.LOCK_EX); e != nil {
		return e
	}
	reservationsPath := filepath.Join(registry, ".registry-reservations.json")
	reservations := installationReservations{Version: 1, Records: map[string]string{}}
	if e = readJSON(reservationsPath, &reservations); e != nil && !os.IsNotExist(e) {
		return e
	}
	if reservations.Version != 1 || reservations.Records == nil {
		return errors.New("invalid local identity reservation registry")
	}
	fd, e := syscall.Open(statePath+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer syscall.Close(fd)
	if e = syscall.Flock(fd, syscall.LOCK_EX); e != nil {
		return e
	}
	var state Installation
	if _, e = os.Stat(statePath); e == nil {
		if e = readJSON(statePath, &state); e != nil {
			return e
		}
		_, createdErr := time.Parse(time.RFC3339Nano, state.Created)
		if state.Version != 1 || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(state.ID) || createdErr != nil || !ValidIMEI(state.IMEI) || state.TAC != state.IMEI[:8] || state.Base["boot.img"] != base["boot.img"] || state.Base["rootfs.ext4"] != base["rootfs.ext4"] || (tac != "" && tac != state.TAC) {
			return errors.New("installation record does not match base/TAC")
		}
	} else if os.IsNotExist(e) {
		if tac == "" {
			tac = DefaultTAC
		}
		used, e := installationNumbers(registry, filepath.Dir(statePath))
		if e != nil {
			return e
		}
		for _, value := range reservations.Records {
			if !ValidIMEI(value) {
				return errors.New("invalid reserved installation identity")
			}
			used[value] = true
		}
		value, e := uniqueIMEI(tac, used, GenerateIMEI)
		if e != nil {
			return e
		}
		id := make([]byte, 16)
		if _, e = rand.Read(id); e != nil {
			return e
		}
		selected := hex.EncodeToString(id)
		if len(chosenID) > 0 && chosenID[0] != "" {
			if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(chosenID[0]) {
				return errors.New("invalid installation UUID")
			}
			selected = chosenID[0]
		}
		state = Installation{1, selected, time.Now().UTC().Format(time.RFC3339Nano), tac, value, base}
		if e = writeJSONAtomic(statePath, state); e != nil {
			return e
		}
	} else {
		return e
	}
	pathHash := sha256.Sum256([]byte(statePath))
	reservationKey := hex.EncodeToString(pathHash[:])
	for key, value := range reservations.Records {
		if key != reservationKey && value == state.IMEI {
			return errors.New("installation identity is already reserved by another local record")
		}
	}
	if prior, ok := reservations.Records[reservationKey]; ok && prior != state.IMEI {
		return errors.New("installation record differs from its local identity reservation")
	}
	reservations.Records[reservationKey] = state.IMEI
	if e = writeJSONAtomic(reservationsPath, reservations); e != nil {
		return e
	}
	if _, e = os.Stat(output); e == nil {
		m, e := verifyManifest(output)
		if e != nil {
			return e
		}
		actual, e := imageDefault(filepath.Join(output, "rootfs.ext4"))
		if e != nil {
			return e
		}
		if m.InstallationID != state.ID || m.Base["rootfs.ext4"] != base["rootfs.ext4"] || actual != state.IMEI {
			return errors.New("output belongs to another installation")
		}
		return nil
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(output), 0700); e != nil {
		return e
	}
	temp, e := os.MkdirTemp(filepath.Dir(output), ".personalize-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(temp)
	for name := range base {
		if e = copyFile(filepath.Join(images, name), filepath.Join(temp, name), 0600); e != nil {
			return e
		}
	}
	if e = injectDefault(filepath.Join(temp, "rootfs.ext4"), temp, state.IMEI); e != nil {
		return e
	}
	if _, e = command("", nil, "e2fsck", "-fn", filepath.Join(temp, "rootfs.ext4")); e != nil {
		return e
	}
	if e = writeJSON(filepath.Join(temp, "installation.json"), state, 0600); e != nil {
		return e
	}
	if e = finalize(temp, Manifest{Version: 1, Personalized: true, InstallationID: state.ID, Base: base}); e != nil {
		return e
	}
	return os.Rename(temp, output)
}
func PersonalizeCLI(p Paths, args []string) error {
	f := flag.NewFlagSet("prepare-install", flag.ContinueOnError)
	images := f.String("images", "", "verified base-image directory")
	state := f.String("installation", "", "new private state file for each installation; reuse on retry")
	output := f.String("output", "", "new external output directory")
	tac := f.String("tac", "", "optional eight-digit TAC (default "+DefaultTAC+")")
	if e := f.Parse(args); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			return nil
		}
		return e
	}
	if *images == "" || *state == "" || *output == "" {
		return errors.New("--images, --installation and --output are required")
	}
	if e := Prepare(p, *images, *state, *output, *tac); e != nil {
		return e
	}
	fmt.Println("PASS: installation images prepared; no device was accessed")
	return nil
}
