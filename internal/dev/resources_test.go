package dev

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResourceCatalogDownloadsAndCachesAfterClone(t *testing.T) {
	p := Paths{Repo: t.TempDir(), Work: t.TempDir()}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.Write([]byte("versioned resource")) }))
	defer server.Close()
	path := filepath.Join(p.Repo, "buildroot-external/board/msm8916/resources.json")
	if e := writeJSON(path, map[string]Resource{"kernel": {URL: server.URL, SHA256: digest([]byte("versioned resource")), File: "runtime.bin"}}, 0600); e != nil {
		t.Fatal(e)
	}
	got, _, e := resourceFile(p, "kernel")
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = resourceFile(p, "kernel"); e != nil || requests != 1 {
		t.Fatal("cache", requests, e)
	}
	if e = os.WriteFile(got, []byte("tampered"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, _, e = resourceFile(p, "kernel"); e == nil {
		t.Fatal("tampered cache accepted")
	}
}
func TestUUIDSSIDAndLocalBackupMetadata(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		id, e := NewInstallationUUID()
		if e != nil || len(id) != 32 || id[12] != '4' || seen[id] {
			t.Fatal("UUID", id, e)
		}
		seen[id] = true
		if got := DefaultSSID(id); got != "microstick-"+id[:8] {
			t.Fatal(got)
		}
	}
	p := Paths{Work: t.TempDir()}
	path := filepath.Join(p.Work, "backups/flasher-20261112T010203.000000000Z")
	mustWrite(t, filepath.Join(path, "BackupManifest.json"), `{"schema_version":1,"complete":true}`)
	mustWrite(t, filepath.Join(path, "payload.bin"), "retained bytes")
	items, e := ListBackupSummaries(p)
	if e != nil || len(items) != 1 || items[0].Bytes < 14 || items[0].Created.Hour() != 1 {
		t.Fatal(items, e)
	}
}
