package dev

import (
	"path/filepath"
	"testing"
)

func TestUpdateBuiltinPluginsPreservesEditsDeletionAndState(t *testing.T) {
	for _, mode := range []string{"unchanged", "edited", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			old := testExt4(t, dir, "old.ext4")
			next := testExt4(t, dir, "next.ext4")
			data := testExt4(t, dir, "data.ext4")
			for _, image := range []string{old, next} {
				for _, path := range []string{"/usr", "/usr/share", "/usr/share/microstick", "/usr/share/microstick/default-plugins", "/usr/share/microstick/default-plugins/web"} {
					if _, e := debugImage(image, "mkdir "+path, true); e != nil {
						t.Fatal(e)
					}
				}
			}
			for _, path := range []string{"/plugins", "/plugins/web", "/plugins/web/.state"} {
				if _, e := debugImage(data, "mkdir "+path, true); e != nil {
					t.Fatal(e)
				}
			}
			local := filepath.Join(dir, "file")
			mustWrite(t, local, "old plugin\n")
			if e := imagePut(old, "/usr/share/microstick/default-plugins/web/run", local, 0100755); e != nil {
				t.Fatal(e)
			}
			if e := imagePut(data, "/plugins/web/run", local, 0100755); e != nil {
				t.Fatal(e)
			}
			for _, pair := range []struct{ image, path string }{{old, "/usr/share/microstick/default-plugins/web/link"}, {next, "/usr/share/microstick/default-plugins/web/link"}, {data, "/plugins/web/link"}} {
				if _, e := debugImage(pair.image, "symlink "+pair.path+" run", true); e != nil {
					t.Fatal(e)
				}
			}
			mustWrite(t, local, "new plugin\n")
			if e := imagePut(next, "/usr/share/microstick/default-plugins/web/run", local, 0100755); e != nil {
				t.Fatal(e)
			}
			mustWrite(t, local, "private plugin history\n")
			if e := imagePut(data, "/plugins/web/.state/history", local, 0100600); e != nil {
				t.Fatal(e)
			}
			saved := filepath.Join(dir, "saved.ext4")
			if mode == "edited" {
				mustWrite(t, local, "custom plugin\n")
				if e := imagePut(data, "/plugins/web/run", local, 0100755); e != nil {
					t.Fatal(e)
				}
			}
			if mode == "deleted" {
				if e := removeImageTree(data, "/plugins/web", 0); e != nil {
					t.Fatal(e)
				}
			}
			if e := copyFile(data, saved, 0600); e != nil {
				t.Fatal(e)
			}
			if e := refreshBuiltinPlugins(old, next, saved, data, "/plugins", dir); e != nil {
				t.Fatal(e)
			}
			if mode == "deleted" {
				if imageExists(data, "/plugins/web") {
					t.Fatal("deleted plugin resurrected")
				}
				return
			}
			want := "new plugin\n"
			if mode == "edited" {
				want = "custom plugin\n"
			}
			b, e := imageBytes(data, "/plugins/web/run")
			if e != nil || string(b) != want {
				t.Fatal("plugin code policy", e)
			}
			b, e = imageBytes(data, "/plugins/web/.state/history")
			if e != nil || string(b) != "private plugin history\n" {
				t.Fatal("plugin state lost", e)
			}
		})
	}
}
