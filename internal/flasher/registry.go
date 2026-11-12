package flasher

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"sort"
	"strings"
	"time"

	"microstick.dev/tools/boards"
)

const EngineVersion = "1.0.0"
const ReleasesURL = "https://github.com/microstick-os/microstick/releases"
const RegistryURL = "https://raw.githubusercontent.com/microstick-os/microstick/board-registry/v1/index.json"
const RegistryLimit = 8 << 20

var boardIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,95}$`)
var sourceCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var engineCapabilities = map[string]bool{"kernel-linux17": true, "own-dsp-v1": true, "payload-v1": true, BootFirmwarePolicy: true, StockRecipe: true, "preserve-gpt-v1": true, "dtb-transform-v1": true}

type ProfileRequirements struct {
	MinFlasherVersion  string   `json:"min_flasher_version"`
	MinFirmwareVersion string   `json:"min_firmware_version"`
	Capabilities       []string `json:"capabilities"`
}
type BoardResource struct {
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	File         string `json:"file"`
	Member       string `json:"member,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	ResultSHA256 string `json:"result_sha256,omitempty"`
}
type BoardVariant struct {
	LegacyProfileSHA256 string              `json:"legacy_profile_sha256,omitempty"`
	Version             int                 `json:"schema_version"`
	Profile             HardwareProfile     `json:"profile"`
	Requires            ProfileRequirements `json:"requires"`
	RuntimeBundle       string              `json:"runtime_bundle"`
	DTBResource         string              `json:"dtb_resource,omitempty"`
	DTBTransform        string              `json:"dtb_transform,omitempty"`
}
type BoardDescriptor struct {
	Version  int            `json:"schema_version"`
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	SoC      string         `json:"soc"`
	Revision uint64         `json:"revision"`
	Status   string         `json:"status"`
	Reason   string         `json:"reason,omitempty"`
	Variants []BoardVariant `json:"variants"`
}
type GPIOChange struct {
	Node       string `json:"node"`
	Controller uint32 `json:"controller"`
	From       uint32 `json:"from"`
	To         uint32 `json:"to"`
	Flags      uint32 `json:"flags"`
}
type DTBTransform struct {
	SourceSHA256 string       `json:"source_sha256"`
	ResultSHA256 string       `json:"result_sha256"`
	GPIOChanges  []GPIOChange `json:"gpio_changes"`
	PinGroup     string       `json:"pin_group"`
}
type RegistryPayload struct {
	Version    int                        `json:"schema_version"`
	Sequence   uint64                     `json:"sequence"`
	Commit     string                     `json:"source_commit"`
	Published  string                     `json:"published_at"`
	Boards     []BoardDescriptor          `json:"boards"`
	Resources  map[string]BoardResource   `json:"resources"`
	Bundles    map[string]json.RawMessage `json:"bundles"`
	Transforms map[string]DTBTransform    `json:"transforms"`
	Revoked    map[string]string          `json:"revoked_profiles,omitempty"`
}
type RegistryEnvelope struct {
	Payload   []byte `json:"payload"`
	KeyID     string `json:"key_id"`
	Signature []byte `json:"signature"`
}
type ProfileReference struct {
	SnapshotSHA256 string `json:"snapshot_sha256"`
	ProfileSHA256  string `json:"profile_sha256"`
	BoardID        string `json:"board_id"`
	VariantID      string `json:"variant_id"`
	Sequence       uint64 `json:"sequence"`
}

// RegistrySnapshot can authorize profiles only after signature verification or
// loading the immutable bootstrap packaged with this executable.
type RegistrySnapshot struct {
	payload RegistryPayload
	proof   []byte
	digest  string
	source  string
	trusted bool
	keys    map[string]ed25519.PublicKey
}
type profileAuthorization struct {
	snapshot        *RegistrySnapshot
	digest          string
	board           BoardDescriptor
	variant         BoardVariant
	reference       ProfileReference
	flasherVersion  string
	firmwareVersion string
	current         *RegistrySnapshot
}
type CompatibilityResult struct{ Status, Message string }

func decodeRegistryJSON(b []byte, v any) error {
	if len(b) > RegistryLimit {
		return errors.New("board registry exceeds size limit")
	}
	if !json.Valid(b) {
		return errors.New("invalid registry JSON")
	}
	// Duplicate keys are ambiguous across JSON implementations and signatures.
	var scan func(*json.Decoder) error
	scan = func(d *json.Decoder) error {
		t, e := d.Token()
		if e != nil {
			return e
		}
		if c, ok := t.(json.Delim); ok {
			switch c {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, e := d.Token()
					if e != nil {
						return e
					}
					s := k.(string)
					if seen[s] {
						return errors.New("duplicate registry JSON key")
					}
					seen[s] = true
					if e = scan(d); e != nil {
						return e
					}
				}
			case '[':
				for d.More() {
					if e = scan(d); e != nil {
						return e
					}
				}
			default:
				return errors.New("unexpected registry delimiter")
			}
			_, e = d.Token()
			return e
		}
		return nil
	}
	if e := scan(json.NewDecoder(bytes.NewReader(b))); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("trailing registry data")
	}
	return nil
}
func ReadBoardSources(source fs.FS) (RegistryPayload, error) {
	p := RegistryPayload{Version: 1, Boards: []BoardDescriptor{}, Resources: map[string]BoardResource{}, Bundles: map[string]json.RawMessage{}, Transforms: map[string]DTBTransform{}}
	for _, item := range []struct {
		path  string
		value any
	}{{"resources.json", &p.Resources}, {"transforms.json", &p.Transforms}} {
		b, e := fs.ReadFile(source, item.path)
		if e != nil {
			return p, e
		}
		if e = decodeRegistryJSON(b, item.value); e != nil {
			return p, e
		}
	}
	names, e := fs.Glob(source, "*/board.json")
	if e != nil {
		return p, e
	}
	sort.Strings(names)
	for _, name := range names {
		b, e := fs.ReadFile(source, name)
		if e != nil {
			return p, e
		}
		var board BoardDescriptor
		if e = decodeRegistryJSON(b, &board); e != nil {
			return p, fmt.Errorf("%s: %w", name, e)
		}
		if strings.Split(name, "/")[0] != board.ID {
			return p, errors.New("board directory and ID differ")
		}
		p.Boards = append(p.Boards, board)
	}
	names, e = fs.Glob(source, "bundles/*.json")
	if e != nil {
		return p, e
	}
	for _, name := range names {
		b, e := fs.ReadFile(source, name)
		if e != nil {
			return p, e
		}
		if !json.Valid(b) {
			return p, errors.New("invalid runtime bundle")
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "bundles/"), ".json")
		p.Bundles[id] = b
	}
	return p, validateRegistry(p)
}
func validateRegistry(p RegistryPayload) error {
	if p.Version != 1 || len(p.Boards) == 0 || len(p.Boards) > 1024 {
		return errors.New("unsupported registry schema or board count")
	}
	ids := map[string]bool{}
	variants := map[string]bool{}
	for _, b := range p.Boards {
		if !boardIDPattern.MatchString(b.ID) || ids[b.ID] || b.Name == "" || b.SoC == "" || b.Revision == 0 || len(b.Variants) == 0 || b.Status != "supported" && b.Status != "candidate" && b.Status != "blocked" || b.Status == "blocked" && b.Reason == "" {
			return fmt.Errorf("invalid board descriptor: %s", b.ID)
		}
		ids[b.ID] = true
		for _, v := range b.Variants {
			if v.LegacyProfileSHA256 != "" && requireHash(v.LegacyProfileSHA256) != nil {
				return errors.New("invalid legacy profile digest")
			}
			q := v.Profile
			if !boardIDPattern.MatchString(q.ID) || variants[q.ID] || v.Version < 1 || b.Version < 1 {
				return errors.New("invalid or duplicate board variant")
			}
			variants[q.ID] = true
			if _, e := compareVersions(v.Requires.MinFlasherVersion, "0.0.0"); e != nil {
				return e
			}
			if _, e := compareVersions(v.Requires.MinFirmwareVersion, "0.0.0"); e != nil {
				return e
			}
			if len(p.Bundles[v.RuntimeBundle]) == 0 {
				return errors.New("runtime bundle missing")
			}
			if v.DTBResource != "" {
				if _, ok := p.Resources[v.DTBResource]; !ok {
					return errors.New("DTB resource missing")
				}
			}
			if v.DTBTransform != "" {
				if _, ok := p.Transforms[v.DTBTransform]; !ok {
					return errors.New("DTB transform missing")
				}
			}
			if b.Status == "supported" && (q.Board == "" || q.Layout == "" || len(q.SourceSets) == 0 || !q.Qualified || q.Status != "qualified") {
				return errors.New("supported board requires reviewed source sets and qualification")
			}
		}
	}
	for _, r := range p.Resources {
		if !strings.HasPrefix(r.URL, "https://") || requireHash(r.SHA256) != nil || r.Bytes <= 0 || r.Bytes > 1<<30 || r.File == "" || strings.ContainsAny(r.File, "/\\") || r.File == "." || r.File == ".." || r.ResultSHA256 != "" && requireHash(r.ResultSHA256) != nil {
			return errors.New("invalid board resource")
		}
		if r.Member != "" && (strings.HasPrefix(r.Member, "/") || strings.Contains(r.Member, "..") || strings.Contains(r.Member, "\\")) {
			return errors.New("invalid resource archive member")
		}
	}
	for _, t := range p.Transforms {
		if requireHash(t.SourceSHA256) != nil || requireHash(t.ResultSHA256) != nil || len(t.GPIOChanges) == 0 || len(t.GPIOChanges) > 32 || t.PinGroup == "" {
			return errors.New("invalid DTB transformation")
		}
	}
	for hash, reason := range p.Revoked {
		if requireHash(hash) != nil || reason == "" {
			return errors.New("invalid profile revocation")
		}
	}
	return nil
}
func BuiltinRegistry() (*RegistrySnapshot, error) {
	p, e := ReadBoardSources(boards.Files)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	return &RegistrySnapshot{payload: p, proof: raw, digest: HashBytes(raw), source: "bundled", trusted: true}, nil
}
func RegistryKeys() (map[string]ed25519.PublicKey, error) {
	raw, e := boards.Files.ReadFile("keys.json")
	if e != nil {
		return nil, e
	}
	var values map[string]string
	if e = decodeRegistryJSON(raw, &values); e != nil {
		return nil, e
	}
	out := map[string]ed25519.PublicKey{}
	for id, s := range values {
		b, e := base64.StdEncoding.DecodeString(s)
		if e != nil || len(b) != ed25519.PublicKeySize {
			return nil, errors.New("invalid registry trust key")
		}
		out[id] = ed25519.PublicKey(b)
	}
	return out, nil
}
func SignRegistry(p RegistryPayload, keyID string, key ed25519.PrivateKey) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize || keyID == "" {
		return nil, errors.New("registry signing key missing")
	}
	if e := validateRegistry(p); e != nil {
		return nil, e
	}
	if p.Sequence == 0 || !sourceCommitPattern.MatchString(p.Commit) {
		return nil, errors.New("published registry requires sequence and source commit")
	}
	if _, e := time.Parse(time.RFC3339, p.Published); e != nil {
		return nil, e
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return nil, e
	}
	return json.Marshal(RegistryEnvelope{Payload: raw, KeyID: keyID, Signature: ed25519.Sign(key, raw)})
}
func VerifyRegistry(raw []byte, keys map[string]ed25519.PublicKey) (*RegistrySnapshot, error) {
	var env RegistryEnvelope
	if e := decodeRegistryJSON(raw, &env); e != nil {
		return nil, e
	}
	key := keys[env.KeyID]
	if len(key) != ed25519.PublicKeySize || !ed25519.Verify(key, env.Payload, env.Signature) {
		return nil, errors.New("board registry signature is invalid or key is unknown")
	}
	var p RegistryPayload
	if e := decodeRegistryJSON(env.Payload, &p); e != nil {
		return nil, e
	}
	if e := validateRegistry(p); e != nil {
		return nil, e
	}
	if p.Sequence == 0 || !sourceCommitPattern.MatchString(p.Commit) {
		return nil, errors.New("invalid registry publication")
	}
	if _, e := time.Parse(time.RFC3339, p.Published); e != nil {
		return nil, e
	}
	anchors := map[string]ed25519.PublicKey{}
	for id, k := range keys {
		anchors[id] = append(ed25519.PublicKey(nil), k...)
	}
	return &RegistrySnapshot{payload: p, proof: append([]byte(nil), raw...), digest: HashBytes(env.Payload), source: "registry", trusted: true, keys: anchors}, nil
}
func (s *RegistrySnapshot) Digest() string    { return s.digest }
func (s *RegistrySnapshot) Sequence() uint64  { return s.payload.Sequence }
func (s *RegistrySnapshot) Published() string { return s.payload.Published }
func (s *RegistrySnapshot) Source() string    { return s.source }
func (s *RegistrySnapshot) Proof() []byte     { return append([]byte(nil), s.proof...) }
func (s *RegistrySnapshot) Profiles(flasherVersion, firmwareVersion string, current *RegistrySnapshot) []HardwareProfile {
	if s == nil || !s.trusted {
		return nil
	}
	if current == nil {
		current = s
	}
	var out []HardwareProfile
	for _, b := range s.payload.Boards {
		for _, v := range b.Variants {
			var p HardwareProfile
			raw, _ := json.Marshal(v.Profile)
			_ = json.Unmarshal(raw, &p)
			ref := ProfileReference{SnapshotSHA256: s.digest, ProfileSHA256: Hash([]any{b.ID, b.Name, b.SoC, b.Version, b.Revision, b.Status, b.Reason, v}), BoardID: b.ID, VariantID: p.ID, Sequence: s.payload.Sequence}
			p.authorization = &profileAuthorization{snapshot: s, digest: Hash(p), board: b, variant: v, reference: ref, flasherVersion: flasherVersion, firmwareVersion: firmwareVersion, current: current}
			out = append(out, p)
		}
	}
	return out
}
func (p HardwareProfile) ProfileReference() *ProfileReference {
	if p.authorization == nil {
		return nil
	}
	r := p.authorization.reference
	return &r
}
func (p HardwareProfile) RegistryProof() []byte {
	if p.authorization == nil {
		return nil
	}
	return p.authorization.snapshot.Proof()
}
func (p HardwareProfile) Resource(id string) (BoardResource, bool) {
	if p.authorization == nil {
		return BoardResource{}, false
	}
	r, ok := p.authorization.snapshot.payload.Resources[id]
	return r, ok
}
func (p HardwareProfile) RuntimeBundle() []byte {
	if p.authorization == nil {
		return nil
	}
	return append([]byte(nil), p.authorization.snapshot.payload.Bundles[p.authorization.variant.RuntimeBundle]...)
}
func (p HardwareProfile) DisplayName() string {
	if p.authorization == nil {
		return p.ID
	}
	return p.authorization.board.Name
}
func (p HardwareProfile) Compatibility() CompatibilityResult {
	a := p.authorization
	if a == nil {
		return CompatibilityResult{}
	}
	if a.board.Status == "blocked" {
		return CompatibilityResult{"blocked", a.board.Reason}
	}
	if reason := a.current.blocked(a.reference); reason != "" {
		return CompatibilityResult{"blocked", reason}
	}
	if a.board.Status != "supported" {
		return CompatibilityResult{"candidate", "Board support is under review"}
	}
	update := func(message string) CompatibilityResult {
		return CompatibilityResult{"flasher-update-required", message + ". " + ReleasesURL}
	}
	if a.board.Version != 1 || a.variant.Version != 1 || p.Version != Schema {
		return update("Профиль платы требует более новой схемы")
	}
	c, e := compareVersions(a.flasherVersion, a.variant.Requires.MinFlasherVersion)
	if e != nil || c < 0 {
		return update(fmt.Sprintf("Обновите flasher: текущая версия %s, требуется %s", a.flasherVersion, a.variant.Requires.MinFlasherVersion))
	}
	for _, capability := range a.variant.Requires.Capabilities {
		if !engineCapabilities[capability] {
			return update("Нужна возможность flasher: " + capability)
		}
	}
	if a.board.SoC != "msm8916" || p.KernelBuild != 17 || p.Recipe != StockRecipe && p.Recipe != "preserve-gpt-v1" || p.BootFirmwarePolicy != BootFirmwarePolicy {
		return update("Для рецепта платы или boot ABI требуется обновить flasher")
	}
	if a.firmwareVersion != "" && a.variant.Requires.MinFirmwareVersion != "0.0.0" {
		c, e = compareVersions(a.firmwareVersion, a.variant.Requires.MinFirmwareVersion)
		if e != nil || c < 0 {
			return CompatibilityResult{"firmware-update-required", fmt.Sprintf("Комплект прошивки: текущая версия %s, требуется %s. %s", a.firmwareVersion, a.variant.Requires.MinFirmwareVersion, ReleasesURL)}
		}
	}
	return CompatibilityResult{}
}
func (s *RegistrySnapshot) blocked(ref ProfileReference) string {
	if s == nil {
		return "registry unavailable"
	}
	if r := s.payload.Revoked[ref.ProfileSHA256]; r != "" {
		return r
	}
	for _, b := range s.payload.Boards {
		if b.ID == ref.BoardID && b.Status == "blocked" {
			return b.Reason
		}
	}
	return ""
}
func (s *RegistrySnapshot) PinnedProfile(raw []byte, ref ProfileReference, flasherVersion, firmwareVersion string) (HardwareProfile, error) {
	original, e := BuiltinRegistry()
	if e != nil {
		return HardwareProfile{}, e
	}
	if !bytes.Equal(raw, original.proof) {
		keys := s.keys
		if len(keys) == 0 {
			keys, e = RegistryKeys()
			if e != nil {
				return HardwareProfile{}, e
			}
		}
		original, e = VerifyRegistry(raw, keys)
		if e != nil {
			return HardwareProfile{}, e
		}
	}
	if original.digest != ref.SnapshotSHA256 {
		return HardwareProfile{}, errors.New("operation registry digest differs")
	}
	for _, p := range original.Profiles(flasherVersion, firmwareVersion, s) {
		if p.ProfileReference() != nil && *p.ProfileReference() == ref {
			if c := p.Compatibility(); c.Status != "" {
				return p, errors.New(c.Message)
			}
			return p, nil
		}
	}
	return HardwareProfile{}, errors.New("pinned board profile missing; restore own backup")
}

// PrepareRegistryPublication preserves tombstones and rejects silent withdrawal
// of supported boards. Source revisions make a reviewed unblock explicit.
func PrepareRegistryPublication(next *RegistryPayload, old *RegistrySnapshot) error {
	if old == nil || !old.trusted {
		return errors.New("previous registry is not trusted")
	}
	next.Sequence = old.Sequence() + 1
	if next.Revoked == nil {
		next.Revoked = map[string]string{}
	}
	for h, r := range old.payload.Revoked {
		next.Revoked[h] = r
	}
	for _, b := range old.payload.Boards {
		found := false
		for _, n := range next.Boards {
			if n.ID != b.ID {
				continue
			}
			found = true
			if n.Revision < b.Revision {
				return errors.New("board revision cannot decrease")
			}
			if b.Status == "blocked" && n.Status != "blocked" && n.Revision <= b.Revision {
				return errors.New("unblocking requires a newer board revision")
			}
			if n.Status == "blocked" {
				for _, v := range b.Variants {
					h := Hash([]any{b.ID, b.Name, b.SoC, b.Version, b.Revision, b.Status, b.Reason, v})
					next.Revoked[h] = n.Reason
				}
			}
		}
		if !found && (b.Status == "supported" || b.Status == "blocked") {
			return errors.New("retain supported/blocked boards with an explicit blocked status")
		}
	}
	return validateRegistry(*next)
}
func (s *RegistrySnapshot) SourceCommit() string { return s.payload.Commit }

func (s *RegistrySnapshot) ContentDigest() string {
	p := s.payload
	p.Sequence = 0
	p.Commit = ""
	p.Published = ""
	return Hash(p)
}
func RegistryContentDigest(p RegistryPayload) string {
	p.Sequence = 0
	p.Commit = ""
	p.Published = ""
	return Hash(p)
}

func (p HardwareProfile) LegacyCompatible() bool {
	return p.authorization != nil && p.authorization.variant.LegacyProfileSHA256 == Hash(p) && canonicalNativeProfile(p)
}
