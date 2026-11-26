package flasher

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func registryFixture(t *testing.T) (RegistryPayload, string, ed25519.PrivateKey, map[string]ed25519.PublicKey) {
	t.Helper()
	base, e := BuiltinRegistry()
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(base.payload)
	var p RegistryPayload
	json.Unmarshal(raw, &p)
	p.Sequence = 1
	p.Commit = strings.Repeat("a", 40)
	p.Published = time.Now().UTC().Format(time.RFC3339)
	pub, priv, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	return p, "test", priv, map[string]ed25519.PublicKey{"test": pub}
}
func signedFixture(t *testing.T, p RegistryPayload, id string, key ed25519.PrivateKey) []byte {
	t.Helper()
	raw, e := SignRegistry(p, id, key)
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestRegistrySignatureAndStructuralValidation(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	raw := signedFixture(t, p, id, key)
	for _, kind := range []string{"valid", "tamper", "unknown-key", "duplicate", "trailing", "oversize", "zero-sequence"} {
		t.Run(kind, func(t *testing.T) {
			data := append([]byte(nil), raw...)
			switch kind {
			case "tamper":
				var v RegistryEnvelope
				json.Unmarshal(data, &v)
				v.Payload = bytes.ReplaceAll(v.Payload, []byte("HMUF02"), []byte("Other!"))
				data, _ = json.Marshal(v)
			case "unknown-key":
				var v RegistryEnvelope
				json.Unmarshal(data, &v)
				v.KeyID = "other"
				data, _ = json.Marshal(v)
			case "duplicate":
				data = append([]byte(`{"key_id":"bad",`), data[1:]...)
			case "trailing":
				data = append(data, []byte(`{}`)...)
			case "oversize":
				data = bytes.Repeat([]byte(" "), RegistryLimit+1)
			case "zero-sequence":
				pp := p
				pp.Sequence = 0
				b, _ := json.Marshal(pp)
				data, _ = json.Marshal(RegistryEnvelope{Payload: b, KeyID: id, Signature: ed25519.Sign(key, b)})
			}
			s, e := VerifyRegistry(data, keys)
			if (e == nil) != (kind == "valid") {
				t.Fatalf("snapshot=%v err=%v", s, e)
			}
		})
	}
}
func TestRegistryAddsBoardWithoutChangingEngine(t *testing.T) {
	f := newFixture(t)
	p, id, key, keys := registryFixture(t)
	if Resolve(f.report, mustCatalog(t)).Status == "matched" {
		t.Fatal("synthetic board already supported")
	}
	profile := fixtureProfile(f.report)
	profile.ID = "synthetic-rootfs"
	profile.Recipe = "preserve-gpt-v1"
	profile.Board = "synthetic"
	profile.Layout = "boot-rootfs"
	profile.BootFirmwarePolicy = BootFirmwarePolicy
	profile.SourceSets = []ReviewedSourceSet{{ID: "source", Fingerprint: SourceSetFingerprint(f.report.Variants), Review: "synthetic fixture"}}
	p.Boards = append(p.Boards, BoardDescriptor{Version: 1, ID: "synthetic", Name: "Synthetic board", SoC: "msm8916", Revision: 1, Variants: []BoardVariant{{Version: 1, Profile: profile, Requires: ProfileRequirements{EngineVersion, "1.0.0", []string{"preserve-gpt-v1"}}, RuntimeBundle: "linux17"}}})
	s, e := VerifyRegistry(signedFixture(t, p, id, key), keys)
	if e != nil {
		t.Fatal(e)
	}
	profiles := s.Profiles(EngineVersion, "1.0.0", nil)
	r := Resolve(f.report, profiles)
	if r.Status != "matched" || r.Profile != profile.ID {
		t.Fatal(r)
	}
	var selected HardwareProfile
	for _, q := range profiles {
		if q.ID == profile.ID {
			selected = q
		}
	}
	if !canonicalNativeProfile(selected) {
		t.Fatal("authenticated new board rejected")
	}
	selected.Bundle["kernel"] = HashBytes([]byte("substitution"))
	if canonicalNativeProfile(selected) {
		t.Fatal("edited profile accepted")
	}
}
func mustCatalog(t *testing.T) []HardwareProfile {
	t.Helper()
	ps, e := Catalog()
	if e != nil {
		t.Fatal(e)
	}
	return ps
}
func TestRegistryCompatibilityGates(t *testing.T) {
	for _, kind := range []string{"valid", "flasher", "firmware", "capability", "schema", "soc", "recipe", "development"} {
		t.Run(kind, func(t *testing.T) {
			p, id, key, keys := registryFixture(t)
			b := &p.Boards[0]
			v := &b.Variants[0]
			want := ""
			fv, wv := EngineVersion, "1.0.0"
			switch kind {
			case "flasher":
				v.Requires.MinFlasherVersion = "2.0.0"
				want = "flasher-update-required"
			case "firmware":
				v.Requires.MinFirmwareVersion = "2.0.0"
				want = "firmware-update-required"
			case "capability":
				v.Requires.Capabilities = append(v.Requires.Capabilities, "future")
				want = "flasher-update-required"
			case "schema":
				v.Version = 2
				want = "flasher-update-required"
			case "soc":
				b.SoC = "other"
				want = "flasher-update-required"
			case "recipe":
				v.Profile.Recipe = "other"
				want = "flasher-update-required"
			case "development":
				fv = "development"
				want = "flasher-update-required"
			}
			s, e := VerifyRegistry(signedFixture(t, p, id, key), keys)
			if e != nil {
				t.Fatal(e)
			}
			q := s.Profiles(fv, wv, nil)[0]
			got := q.Compatibility()
			if got.Status != want {
				t.Fatalf("%+v want %s", got, want)
			}
			if strings.Contains(want, "update") && !strings.Contains(got.Message, ReleasesURL) {
				t.Fatal("release link missing")
			}
		})
	}
}
func TestRegistryCacheAndRollback(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	raw := signedFixture(t, p, id, key)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write(raw) }))
	defer server.Close()
	c, e := openRegistryClient(t.TempDir(), server.URL, keys, server.Client())
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	digest := c.Current().Digest()
	if c.Current().Sequence() != 1 {
		t.Fatal("not updated")
	}
	p.Sequence = 2
	p.Boards[0].Name = "HMUF02 updated"
	raw = signedFixture(t, p, id, key)
	if e = c.Refresh(context.Background()); e != nil {
		t.Fatal(e)
	}
	if c.Current().Digest() == digest {
		t.Fatal("new snapshot not selected")
	}
	pp := p
	pp.Sequence = 1
	if e = c.Import(signedFixture(t, pp, id, key)); e == nil {
		t.Fatal("rollback accepted")
	}
	pp = p
	pp.Boards[0].Name = "conflicting"
	if e = c.Import(signedFixture(t, pp, id, key)); e == nil {
		t.Fatal("same sequence changed")
	}
	raw = []byte("invalid")
	if c.Refresh(context.Background()) == nil || c.Current().Sequence() != 2 || c.Warning() == "" {
		t.Fatal("bad update destroyed cache")
	}
	server.Close()
	if c.Refresh(context.Background()) == nil || c.Current().Sequence() != 2 {
		t.Fatal("offline cache unavailable")
	}
	again, e := openRegistryClient(c.root, server.URL, keys, server.Client())
	if e != nil || again.Current().Sequence() != 2 {
		t.Fatal("cache reload", e)
	}
	if requests != 3 {
		t.Fatal("unexpected request count", requests)
	}
}
func TestRegistryMatchingUsesSourceData(t *testing.T) {
	f := newFixture(t)
	var e error
	for _, kind := range []string{"exact", "guid", "capacity", "missing-area", "map", "issue", "selector", "extra", "missing", "layout", "missing-recipe", "two-matches", "new-gpio", "private-metadata"} {
		t.Run(kind, func(t *testing.T) {
			r := f.report
			r.Variants = append([]DTVariant(nil), r.Variants...)
			profile := fixtureProfile(r)
			profile.Board = "synthetic"
			profile.Layout = "boot-rootfs"
			profile.SourceSets = []ReviewedSourceSet{{ID: "source", Fingerprint: SourceSetFingerprint(r.Variants), CompatibilityFingerprint: CompatibilitySetFingerprint(r.Variants), Review: "synthetic fixture"}}
			ps := []HardwareProfile{profile}
			switch kind {
			case "guid":
				r.GPT.DiskGUID = "other"
			case "capacity":
				r.Device.Geometry = Geometry{512, map[string]uint64{"user": 64 << 20, "boot1": 4 << 20, "boot2": 4 << 20}}
			case "missing-area":
				r.Device.Geometry = Geometry{512, map[string]uint64{"user": 64 << 20}}
			case "map":
				r.Variants[0].MapSHA256 = HashBytes(nil)
				r.Variants[0].CompatibilitySHA256 = HashBytes(nil)
			case "issue":
				r.Variants[0].Issues = []string{"new conflict"}
			case "selector":
				r.Variants[0].Selectors = [][]uint32{{99}}
			case "extra":
				r.Variants = append(r.Variants, r.Variants[0])
			case "missing":
				r.Variants = nil
			case "layout":
				r.GPT = PartitionTable{}
			case "missing-recipe":
				ps[0].Recipe = ""
			case "two-matches":
				other := profile
				other.ID = "other"
				ps = append(ps, other)
			case "new-gpio":
				r.Variants[0], e = ParseDTB(makeDT(t, `other { gpios=<&gpio 72 0>; };`), "boot", 0)
				if e != nil {
					t.Fatal(e)
				}
			case "private-metadata":
				data := bytes.ReplaceAll(makeDT(t, ""), []byte("PRIVATE-SERIAL-SENTINEL"), []byte("ANOTHER-SERIAL-SENTINEL"))
				r.Variants[0], e = ParseDTB(data, "boot", 0)
				if e != nil {
					t.Fatal(e)
				}
			}
			got := Resolve(r, ps)
			want := kind == "exact" || kind == "guid" || kind == "capacity" || kind == "private-metadata" || kind == "extra"
			if (got.Status == "matched") != want {
				t.Fatalf("%s: %+v", kind, got)
			}
		})
	}
}
func TestRegistryRawJSONCannotAuthorizeNativeProfile(t *testing.T) {
	ps := mustCatalog(t)
	raw, _ := json.Marshal(ps[0])
	var copy HardwareProfile
	json.Unmarshal(raw, &copy)
	if canonicalNativeProfile(copy) {
		t.Fatal("JSON granted write authorization")
	}
	q := ps[0]
	q.BootFirmwarePolicy = ""
	if canonicalNativeProfile(q) {
		t.Fatal("edited boot contract accepted")
	}
}
func TestRegistryImportRejectsSymlinkCache(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "external")
	os.WriteFile(path, []byte("private"), 0600)
	root := filepath.Join(dir, "cache")
	os.Mkdir(root, 0700)
	os.Symlink(path, filepath.Join(root, "current.json"))
	if _, e := openRegistryClient(root, "", keys, &http.Client{}); e == nil {
		t.Fatal("symlink cache accepted")
	}
	before, _ := os.ReadFile(path)
	c := &RegistryClient{root: root, keys: keys}
	if e := c.Import(signedFixture(t, p, id, key)); e == nil {
		t.Fatal("symlink replaced")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("external file changed")
	}
}
func TestRegistrySemver(t *testing.T) {
	for _, v := range []struct {
		a, b string
		want int
	}{{"1.0.0", "1.0.0", 0}, {"1.10.0", "1.9.0", 1}, {"1.0.0-rc.1", "1.0.0", -1}, {"1.0.0-9", "1.0.0-10", -1}, {"1.0.0+a", "1.0.0+b", 0}, {"1.0.0-alpha", "1.0.0-alpha.1", -1}} {
		got, e := compareVersions(v.a, v.b)
		if e != nil || got != v.want {
			t.Fatal(v, got, e)
		}
	}
	for _, s := range []string{"development", "1.0", "01.0.0", "1.0.0-01"} {
		if _, e := compareVersions(s, "1.0.0"); e == nil {
			t.Fatal("invalid version", s)
		}
	}
}

func TestRegistryProfilesAreImmutable(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	s, e := VerifyRegistry(signedFixture(t, p, id, key), keys)
	if e != nil {
		t.Fatal(e)
	}
	first := s.Profiles(EngineVersion, "", nil)[0]
	ref := *first.ProfileReference()
	first.Bundle["kernel"] = HashBytes(nil)
	first.SourceSets[0].Review = "changed"
	first.RequiredAreas[0] = "unknown"
	next := s.Profiles(EngineVersion, "", nil)[0]
	if *next.ProfileReference() != ref || !canonicalNativeProfile(next) {
		t.Fatal("a caller changed the authenticated snapshot")
	}
	raw := s.Proof()
	raw[0] ^= 1
	if !canonicalNativeProfile(next) {
		t.Fatal("proof copy changed snapshot")
	}
}
func TestRegistryRetryUsesOriginalProofAndCurrentMembership(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	raw := signedFixture(t, p, id, key)
	original, e := VerifyRegistry(raw, keys)
	if e != nil {
		t.Fatal(e)
	}
	q := original.Profiles(EngineVersion, "", nil)[0]
	ref := *q.ProfileReference()
	p.Sequence++
	p.Boards[0].Variants[0].Requires.MinFlasherVersion = "2.0.0"
	current, e := VerifyRegistry(signedFixture(t, p, id, key), keys)
	if e != nil {
		t.Fatal(e)
	}
	restored, e := current.PinnedProfile(raw, ref, EngineVersion, "")
	if e != nil || *restored.ProfileReference() != ref || !canonicalNativeProfile(restored) {
		t.Fatal("retry changed original inputs", e)
	}
	wrong := ref
	wrong.ProfileSHA256 = HashBytes(nil)
	if _, e = current.PinnedProfile(raw, wrong, EngineVersion, ""); e == nil {
		t.Fatal("edited reference accepted")
	}
	if _, e = current.PinnedProfile(append(raw, byte('x')), ref, EngineVersion, ""); e == nil {
		t.Fatal("damaged evidence accepted")
	}
	p.Sequence++
	p.Boards = nil
	withdrawn, e := VerifyRegistry(signedFixture(t, p, id, key), keys)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = withdrawn.PinnedProfile(raw, ref, EngineVersion, ""); e == nil {
		t.Fatal("removed profile used by retry")
	}
}
func TestRegistryPublicationCanRemoveBoards(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	old, e := VerifyRegistry(signedFixture(t, p, id, key), keys)
	if e != nil {
		t.Fatal(e)
	}
	p.Boards = nil
	if e = PrepareRegistryPublication(&p, old.Sequence()); e != nil {
		t.Fatal(e)
	}
	if p.Sequence != 2 {
		t.Fatal("catalog revision not advanced")
	}
	next, e := VerifyRegistry(signedFixture(t, p, id, key), keys)
	if e != nil || len(next.Profiles(EngineVersion, "", nil)) != 0 {
		t.Fatal("removed entries retained", e)
	}
}
func TestRegistryRecognizeStockImageDoesNotRequireBootAreas(t *testing.T) {
	f := newFixture(t)
	r := f.report
	r.Device.Geometry = Geometry{512, map[string]uint64{"user": 64 << 20}}
	p := fixtureProfile(r)
	p.Board = "synthetic"
	p.Layout = "boot-rootfs"
	p.SourceSets = []ReviewedSourceSet{{ID: "source", Fingerprint: SourceSetFingerprint(r.Variants), Review: "synthetic fixture"}}
	if Resolve(r, []HardwareProfile{p}).Status == "matched" {
		t.Fatal("installation accepted missing physical areas")
	}
	if got := Recognize(r, []HardwareProfile{p}); got.Status != "matched" {
		t.Fatal("read-only stock inspection requires unrelated boot areas", got)
	}
}

func TestRegistryAuthenticatedBoardBuildsAndExecutesBoundedPlan(t *testing.T) {
	f := newFixture(t)
	r := addTestFirmware(t, f, 64, 64, 0)
	f.d.info.Identity.Kind = "storage"
	r.Device = f.d.info
	p, id, key, keys := registryFixture(t)
	profile := fixtureProfile(r)
	profile.ID = "synthetic-install"
	profile.Recipe = "preserve-gpt-v1"
	profile.Board = "synthetic-install"
	profile.Layout = "boot-rootfs"
	profile.BootFirmwarePolicy = BootFirmwarePolicy
	archive, e := ExportBoardReport(testCtx, f.d, r, t.TempDir(), nil)
	if e != nil {
		t.Fatal(e)
	}
	var support BoardSupport
	decodeReportFile(t, readReportArchive(t, archive), "SUPPORT.json", &support)
	profile.SourceSets = []ReviewedSourceSet{{ID: "source", Fingerprint: support.SourceSetFingerprint}}
	p.Boards = append(p.Boards, BoardDescriptor{Version: 1, ID: "synthetic-install", Name: "Synthetic installation", SoC: "msm8916", Revision: 1, Variants: []BoardVariant{{Version: 1, Profile: profile, Requires: ProfileRequirements{EngineVersion, "0.0.0", []string{"preserve-gpt-v1", "payload-v1"}}, RuntimeBundle: "linux17"}}})
	raw := signedFixture(t, p, id, key)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	client, e := openRegistryClient(t.TempDir(), server.URL, keys, server.Client())
	if e != nil {
		t.Fatal(e)
	}
	if got := Resolve(r, client.Current().Profiles(EngineVersion, "", nil)); got.Status == "matched" {
		t.Fatal("board already present")
	}
	if e = client.Refresh(testCtx); e != nil {
		t.Fatal(e)
	}
	for _, q := range client.Current().Profiles(EngineVersion, "", nil) {
		if q.ID == profile.ID {
			profile = q
		}
	}
	r.Resolution = Resolve(r, []HardwareProfile{profile})
	if r.Resolution.Status != "matched" {
		t.Fatal(r.Resolution)
	}
	root, e := os.ReadFile(filepath.Join(f.root, "fs.ext4"))
	if e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(f.images, "rootfs.ext4"), root)
	if e = CreatePayload(testCtx, f.images, f.mapper); e != nil {
		t.Fatal(e)
	}
	writeTest(t, filepath.Join(f.images, "Preparation.json"), []byte("synthetic preparation receipt\n"))
	receipt, _ := HashFile(filepath.Join(f.images, "Preparation.json"))
	fw, e := InspectBootFirmware(testCtx, f.d, r, BootFirmwarePolicy)
	if e != nil {
		t.Fatal(e)
	}
	att := ImageAttestation{Version: Schema, Device: r.Device, Profile: profile.ID, Registry: profile.ProfileReference(), Bundle: profile.Bundle, Images: map[string]string{}, BootFirmware: fw, ReceiptSHA256: receipt}
	for _, name := range []string{"boot.img", "rootfs.ext4", "Payload.json"} {
		att.Images[name], e = HashFile(filepath.Join(f.images, name))
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = SaveJSON(filepath.Join(f.images, "HardwareInputs.json"), att); e != nil {
		t.Fatal(e)
	}
	plan, e := BuildProfileInstallPlan(testCtx, f.d, r, profile, f.images, "y", "", f.mapper)
	if e != nil {
		t.Fatal(e)
	}
	if plan.Registry == nil || *plan.Registry != *profile.ProfileReference() {
		t.Fatal("plan is not bound to registry")
	}
	journal := filepath.Join(f.root, "registry-install.jsonl")
	if e = Execute(testCtx, f.d, plan, f.images, "", journal, Hash(plan), nil, f.mapper); e != nil {
		t.Fatal(e)
	}
	if _, e = ResumePlan(testCtx, f.d, plan, profile); e != nil {
		t.Fatal("original profile cannot resume", e)
	}
	edited := plan
	edited.Registry = nil
	if _, e = ResumePlan(testCtx, f.d, edited, profile); e == nil {
		t.Fatal("dynamic profile resumed without its registry")
	}

}

func TestRegistryRefreshesOnEveryAttempt(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	raw := signedFixture(t, p, id, key)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == `"snapshot"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"snapshot"`)
		w.Write(raw)
	}))
	defer server.Close()
	c, e := openRegistryClient(t.TempDir(), server.URL, keys, server.Client())
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Refresh(testCtx); e != nil {
		t.Fatal(e)
	}
	if e = c.Refresh(testCtx); e != nil {
		t.Fatal(e)
	}
	if requests != 2 {
		t.Fatal("catalog was not reread", requests)
	}
	if e = c.Refresh(testCtx); e != nil || requests != 3 || c.Current().Sequence() != 1 {
		t.Fatal("conditional refresh", e, requests)
	}
}

func TestRegistryUnreadableCacheFallsBackAndCanRefresh(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	raw := signedFixture(t, p, id, key)
	root := t.TempDir()
	writeTest(t, filepath.Join(root, "current.json"), []byte(`{"incompatible":true}`))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	client, e := openRegistryClient(root, server.URL, keys, server.Client())
	if e != nil || client.Current().Source() != "bundled" {
		t.Fatal("cache prevented startup", e)
	}
	if e = client.Refresh(testCtx); e != nil || client.Current().Sequence() != 1 {
		t.Fatal("catalog could not replace unreadable cache", e)
	}
}

func TestRegistryPublicationHeaderDoesNotInterpretProfiles(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	raw := signedFixture(t, p, id, key)
	info, e := VerifyRegistryPublication(raw, keys)
	if e != nil || info.Sequence != p.Sequence || info.Commit != p.Commit || info.Content != RegistryContentDigest(p) {
		t.Fatal("publication header", e)
	}
	var env RegistryEnvelope
	json.Unmarshal(raw, &env)
	var payload map[string]any
	json.Unmarshal(env.Payload, &payload)
	payload["boards"] = []any{map[string]any{"future_field": "opaque"}}
	env.Payload, _ = json.Marshal(payload)
	env.Signature = ed25519.Sign(key, env.Payload)
	raw, _ = json.Marshal(env)
	if _, e = VerifyRegistryPublication(raw, keys); e != nil {
		t.Fatal("publication tried to read device-profile structure", e)
	}
	if _, e = VerifyRegistry(raw, keys); e == nil {
		t.Fatal("incomplete catalog loaded for installation")
	}
}

func TestRegistryRefreshReplacesSignedStatusCatalog(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	var fields map[string]any
	original, _ := json.Marshal(p)
	json.Unmarshal(original, &fields)
	for _, board := range fields["boards"].([]any) {
		board.(map[string]any)["status"] = "supported"
	}
	oldPayload, _ := json.Marshal(fields)
	old, _ := json.Marshal(RegistryEnvelope{Payload: oldPayload, KeyID: id, Signature: ed25519.Sign(key, oldPayload)})
	if _, e := VerifyRegistry(old, keys); e == nil || !strings.Contains(e.Error(), "status") {
		t.Fatal("old status catalog was accepted", e)
	}
	previous, e := VerifyRegistryPublication(old, keys)
	if e != nil {
		t.Fatal("publisher could not read old publication header", e)
	}
	if e = PrepareRegistryPublication(&p, previous.Sequence); e != nil {
		t.Fatal(e)
	}
	next := signedFixture(t, p, id, key)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(next) }))
	defer server.Close()
	root := t.TempDir()
	writeTest(t, filepath.Join(root, "current.json"), old)
	c, e := openRegistryClient(root, server.URL, keys, server.Client())
	if e != nil || c.Current().Source() != "bundled" {
		t.Fatal("old cache blocked startup", e)
	}
	if e = c.Refresh(testCtx); e != nil || c.Current().Sequence() != 2 || c.Warning() != "" {
		t.Fatal("current publication did not replace old cache", e, c.Warning())
	}
	cached, e := readRegistryFile(filepath.Join(root, "current.json"))
	if e != nil || !bytes.Equal(cached, next) {
		t.Fatal("updated cache differs", e)
	}
}

func TestRegistryCacheLockRespectsCancellation(t *testing.T) {
	p, id, key, keys := registryFixture(t)
	raw := signedFixture(t, p, id, key)
	root := t.TempDir()
	lock, e := os.OpenFile(filepath.Join(root, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		t.Fatal(e)
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(raw) }))
	defer server.Close()
	c, e := openRegistryClient(root, server.URL, keys, server.Client())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(testCtx, 40*time.Millisecond)
	defer cancel()
	if e = c.Refresh(ctx); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("cache lock ignored deadline", e)
	}
	if c.Current().Source() != "bundled" {
		t.Fatal("partial snapshot published")
	}
}
