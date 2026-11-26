package dev

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"microstick.dev/tools/internal/flasher"
)

func TestBoardResourcesAreBoundedAndCached(t *testing.T) {
	for _, kind := range []string{"valid", "short", "long", "hash", "status", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			body := []byte("reviewed DTB resource")
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch kind {
				case "short":
					w.Write(body[:len(body)-1])
				case "long":
					w.Write(append(body, byte('x')))
				case "hash":
					w.Write(bytes.Repeat([]byte("x"), len(body)))
				case "status":
					w.WriteHeader(404)
				default:
					w.Write(body)
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "resource")
			r := flasher.BoardResource{URL: server.URL, SHA256: digest(body), Bytes: int64(len(body)), File: "resource"}
			if kind == "symlink" {
				target := filepath.Join(t.TempDir(), "outside")
				os.WriteFile(target, body, 0600)
				os.Symlink(target, path)
			}
			e := fetchBoardResourceWithClient(path, r, server.Client())
			if (e == nil) != (kind == "valid") {
				t.Fatal(kind, e)
			}
			if kind == "valid" {
				server.Close()
				if e = fetchBoardResourceWithClient(path, r, server.Client()); e != nil {
					t.Fatal("offline cached resource", e)
				}
				os.WriteFile(path, []byte("damaged"), 0600)
				if e = fetchBoardResourceWithClient(path, r, server.Client()); e == nil {
					t.Fatal("damaged cache accepted")
				}
			} else if kind != "symlink" {
				if _, e = os.Stat(path); !os.IsNotExist(e) {
					t.Fatal("failed download published")
				}
			}
		})
	}
}
func TestBoardsCLIValidatesWithoutDeviceAndProtectsPrivateKey(t *testing.T) {
	p := testPaths(t)
	p.Work = t.TempDir()
	if e := BoardsCLI(p, []string{"validate"}); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(p.Work, "registry.seed")
	if e := BoardsCLI(p, []string{"keygen", "--key-id", "fixture", "--output", path}); e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(path)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("key file permissions", e)
	}
	before, _ := os.ReadFile(path)
	if e = BoardsCLI(p, []string{"keygen", "--key-id", "fixture", "--output", path}); e == nil {
		t.Fatal("signing key overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("key bytes changed")
	}
}
func TestOperationProfilePreservesBootstrapProof(t *testing.T) {
	original, e := flasher.BuiltinRegistry()
	if e != nil {
		t.Fatal(e)
	}
	p := original.Profiles(flasher.EngineVersion, "", nil)[0]
	op := &FlasherOperation{Registry: p.ProfileReference(), RegistryProof: p.RegistryProof()}
	got, e := OperationProfile(op, original, flasher.EngineVersion)
	if e != nil || *got.ProfileReference() != *op.Registry {
		t.Fatal("operation binding", e)
	}
	op.RegistryProof = append(op.RegistryProof, byte('x'))
	if _, e = OperationProfile(op, original, flasher.EngineVersion); e == nil {
		t.Fatal("damaged proof accepted")
	}
}
func TestFirmwareMinimumStopsBeforeBackupOrDeviceRead(t *testing.T) {
	p := testPaths(t)
	payload, e := flasher.ReadBoardSources(os.DirFS(filepath.Join(p.Repo, "boards")))
	if e != nil {
		t.Fatal(e)
	}
	payload.Sequence = 1
	payload.Commit = strings.Repeat("a", 40)
	payload.Published = time.Now().UTC().Format(time.RFC3339)
	payload.Boards[0].Variants[0].Requires.MinFirmwareVersion = "2.0.0"
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := flasher.SignRegistry(payload, "fixture", key)
	if e != nil {
		t.Fatal(e)
	}
	registry, e := flasher.VerifyRegistry(raw, map[string]ed25519.PublicKey{"fixture": pub})
	if e != nil {
		t.Fatal(e)
	}
	profile := registry.Profiles(flasher.EngineVersion, "", nil)[0]
	op := &FlasherOperation{Path: filepath.Join(t.TempDir(), "op.json"), Release: FlasherRelease{Version: "1.0.0"}}
	e = RunFlasherOperation(context.Background(), Paths{}, nil, "", op, profile, WizardOptions{}, nil)
	if e == nil || !strings.Contains(e.Error(), flasher.ReleasesURL) {
		t.Fatal("firmware minimum did not stop preparation", e)
	}
	if _, e = os.Stat(op.Path); !os.IsNotExist(e) {
		t.Fatal("operation was saved before compatibility check")
	}
}
