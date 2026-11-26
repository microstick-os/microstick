package dev

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"microstick.dev/tools/internal/flasher"
)

// BoardsCLI validates source descriptors or publishes a signed metadata snapshot.
// It never opens a device or copies owner-specific firmware into the registry.
func BoardsCLI(p Paths, args []string) error {
	if len(args) == 0 {
		return errors.New("boards requires validate, publish, status, update or import")
	}
	action := args[0]
	f := flag.NewFlagSet("boards "+action, flag.ContinueOnError)
	source := f.String("source", filepath.Join(p.Repo, "boards"), "board source directory")
	output := f.String("output", "", "new external signed snapshot file")
	keyFile := f.String("key-file", "", "external Ed25519 seed file (base64)")
	keyID := f.String("key-id", "", "trusted signing key ID")
	commit := f.String("commit", "", "source commit")
	previous := f.String("previous", "", "previous signed snapshot")
	input := f.String("input", "", "signed snapshot to import")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected boards arguments")
	}
	if action == "verify" {
		raw, e := os.ReadFile(*input)
		if e != nil {
			return e
		}
		keys, e := flasher.RegistryKeys()
		if e != nil {
			return e
		}
		s, e := flasher.VerifyRegistryPublication(raw, keys)
		if e != nil {
			return e
		}
		fmt.Println(s.Commit)
		return nil
	}
	if action == "keygen" {
		if *output == "" || *keyID == "" {
			return errors.New("keygen requires --output and --key-id")
		}
		path, e := ExternalPath(p.Repo, *output)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		pub, key, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			return e
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = f.WriteString(base64.StdEncoding.EncodeToString(key.Seed()) + "\n")
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e != nil {
			return e
		}
		public, _ := json.MarshalIndent(map[string]string{*keyID: base64.StdEncoding.EncodeToString(pub)}, "", "  ")
		fmt.Println(string(public))
		return nil
	}
	if action == "validate" || action == "publish" {
		payload, e := flasher.ReadBoardSources(os.DirFS(*source))
		if e != nil {
			return e
		}
		if action == "validate" {
			fmt.Printf("%d board descriptors validated\n", len(payload.Boards))
			return nil
		}
		if *output == "" || *keyFile == "" || *keyID == "" || *commit == "" {
			return errors.New("publish requires --output, --key-file, --key-id and --commit")
		}
		out, e := ExternalPath(p.Repo, *output)
		if e != nil {
			return e
		}
		seedPath, e := ExternalPath(p.Repo, *keyFile)
		if e != nil {
			return e
		}
		raw, e := os.ReadFile(seedPath)
		if e != nil {
			return e
		}
		seed, e := base64.StdEncoding.DecodeString(string(bytesTrimSpace(raw)))
		if e != nil || len(seed) != ed25519.SeedSize {
			return errors.New("expected a base64 Ed25519 seed")
		}
		key := ed25519.NewKeyFromSeed(seed)
		keys, e := flasher.RegistryKeys()
		if e != nil {
			return e
		}
		if string(keys[*keyID]) != string(key.Public().(ed25519.PublicKey)) {
			return errors.New("signing key does not match boards/keys.json")
		}
		payload.Sequence = 1
		payload.Commit = *commit
		payload.Published = time.Now().UTC().Format(time.RFC3339)
		if *previous != "" {
			proof, e := os.ReadFile(*previous)
			if e != nil {
				return e
			}
			old, e := flasher.VerifyRegistryPublication(proof, keys)
			if e != nil {
				return e
			}
			if e = flasher.PrepareRegistryPublication(&payload, old.Sequence); e != nil {
				return e
			}
			if flasher.RegistryContentDigest(payload) == old.Content {
				return nil
			}
		}
		proof, e := flasher.SignRegistry(payload, *keyID, key)
		if e != nil {
			return e
		}
		if _, e = flasher.VerifyRegistry(proof, keys); e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(out), 0700); e != nil {
			return e
		}
		fd, e := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		_, e = fd.Write(proof)
		if e == nil {
			e = fd.Sync()
		}
		ce := fd.Close()
		if e == nil {
			e = ce
		}
		return e
	}
	client, e := flasher.NewRegistryClient(p.Work)
	if e != nil {
		return e
	}
	switch action {
	case "status":
	case "update":
		if e = client.Refresh(context.Background()); e != nil {
			return e
		}
	case "import":
		if e = client.ImportFile(*input); e != nil {
			return e
		}
	default:
		return errors.New("unknown boards action")
	}
	fmt.Printf("%s sequence=%d published=%s sha256=%s\n", client.Current().Source(), client.Current().Sequence(), client.Current().Published(), client.Current().Digest())
	return nil
}
func bytesTrimSpace(b []byte) []byte { return []byte(strings.TrimSpace(string(b))) }
