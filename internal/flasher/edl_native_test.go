package flasher

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func compileNativePlanHarness(t *testing.T) string {
	t.Helper()
	root := os.Getenv("MICROSTICK_WORKSPACE")
	source := filepath.Join(root, "host-tools/src/qdl-c72e7d152acd605de19b64ad4a605ae5b58a7cda")
	if _, e := os.Stat(filepath.Join(source, "src/sha2.c")); e != nil {
		t.Fatal("pinned QDL sources required", e)
	}
	dir := t.TempDir()
	harness := filepath.Join(dir, "harness.c")
	repo, e := filepath.Abs("../..")
	if e != nil {
		t.Fatal(e)
	}
	code, e := os.ReadFile(filepath.Join(repo, "scripts/native/edl-file-test.c"))
	if e != nil {
		t.Fatal(e)
	}
	writeTest(t, harness, code)
	out := filepath.Join(dir, "harness")
	runTest(t, "cc", "-O1", "-Wall", "-Wextra", "-Werror", "-fsanitize=address,undefined", "-fno-sanitize-recover=all", "-fno-pie", "-no-pie", "-I"+filepath.Join(repo, "scripts/native"), "-I"+filepath.Join(source, "src"), harness, filepath.Join(source, "src/sha2.c"), "-o", out)
	return out
}
func TestNativeExactPlanWriteReadbackAndGates(t *testing.T) {
	for _, kind := range []string{"success", "unarmed", "modified-before-grant", "modified-after-grant", "wrong-input", "wrong-range", "replay", "changed-plan", "interrupt", "readback"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
			t.Setenv("UBSAN_OPTIONS", "halt_on_error=1")
			dir := t.TempDir()
			image := filepath.Join(dir, "user")
			writeTest(t, image, make([]byte, 1<<20))
			t.Setenv("MICROSTICK_NATIVE_TEST_IMAGE", image)
			helper := compileNativePlanHarness(t)
			d, e := OpenEDL(context.Background(), helper, "unused", "")
			if e != nil {
				t.Fatal(e)
			}
			defer d.Close()
			payload := bytes.Repeat([]byte{0x6d}, 32768)
			p := nativeTestPlan(t, d, image, dir, payload)
			rng := p.Operations[0].Range
			approved := Hash(p)
			w, e := NewEDLWriter(d, p, approved)
			if e != nil {
				t.Fatal(e)
			}
			mutate := func() {
				f, e := os.OpenFile(image, os.O_WRONLY, 0)
				if e != nil {
					t.Fatal(e)
				}
				defer f.Close()
				if _, e = f.WriteAt([]byte{1}, int64(rng.Offset)); e != nil {
					t.Fatal(e)
				}
			}
			if kind == "interrupt" || kind == "readback" {
				t.Setenv("MICROSTICK_NATIVE_TEST_FAULT", kind)
				d.Close()
				d, e = OpenEDL(testCtx, helper, "unused", "")
				if e != nil {
					t.Fatal(e)
				}
				defer d.Close()
				w, e = NewEDLWriter(d, p, approved)
				if e != nil {
					t.Fatal(e)
				}
			}
			if kind == "success" || kind == "interrupt" || kind == "readback" {
				writeTest(t, filepath.Join(dir, "payload"), payload)
				e = Execute(testCtx, w, p, dir, "", filepath.Join(dir, "journal.jsonl"), approved, nil)
				if kind != "success" {
					if e == nil {
						t.Fatal("native fault accepted")
					}
					return
				}
				if e != nil {
					t.Fatal(e)
				}
				got, e := HashRange(testCtx, d, rng)
				if e != nil || got != p.Operations[0].SHA256 {
					t.Fatal("native readback wrong", e)
				}
				return
			}
			if kind == "unarmed" {
				if e = w.Write(testCtx, rng, bytes.NewReader(payload)); e == nil {
					t.Fatal("unarmed write accepted")
				}
				return
			}
			if kind == "modified-before-grant" {
				mutate()
			}
			if kind == "changed-plan" {
				p.Operations[0].Range.Offset += 512
			}
			e = w.authorize(testCtx, p, approved)
			if kind == "changed-plan" {
				if e == nil {
					t.Fatal("changed plan granted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			if kind == "modified-after-grant" {
				mutate()
			}
			if kind == "wrong-input" {
				payload[0] ^= 1
			}
			if kind == "wrong-range" {
				rng.Offset += 512
			}
			e = w.Write(testCtx, rng, bytes.NewReader(payload))
			if kind == "replay" {
				if e != nil {
					t.Fatal(e)
				}
				e = w.Write(testCtx, rng, bytes.NewReader(payload))
			}
			if e == nil {
				t.Fatalf("native gate %s failed", kind)
			}
		})
	}
}
func TestNativeWriterRejectsForeignPlanIdentity(t *testing.T) {
	p := InstallPlan{Device: DeviceInfo{Identity: Identity{"simulator", "x"}}}
	if _, e := NewEDLWriter(&EDLReader{}, p, strings.Repeat("a", 64)); e == nil {
		t.Fatal(fmt.Sprint("foreign native plan accepted"))
	}
}

func TestNativeOccupiedGrantFaultsAndCompletedReceipt(t *testing.T) {
	for _, kind := range []string{"success", "wrong-input", "wrong-range", "interrupt", "readback", "replay", "completed"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("ASAN_OPTIONS", "detect_leaks=0:abort_on_error=1")
			t.Setenv("UBSAN_OPTIONS", "halt_on_error=1")
			dir := t.TempDir()
			image := filepath.Join(dir, "user")
			writeTest(t, image, make([]byte, 1<<20))
			t.Setenv("MICROSTICK_NATIVE_TEST_IMAGE", image)
			if kind == "interrupt" || kind == "readback" {
				t.Setenv("MICROSTICK_NATIVE_TEST_FAULT", kind)
			}
			d, e := OpenEDL(testCtx, compileNativePlanHarness(t), "unused", "")
			if e != nil {
				t.Fatal(e)
			}
			defer d.Close()
			payload := bytes.Repeat([]byte{0x4c}, 32768)
			p := nativeTestPlan(t, d, image, dir, payload)
			p.PayloadVersion = 1
			p.PayloadSHA256 = HashBytes([]byte("synthetic occupied descriptor"))
			rng := p.Operations[0].Range
			w, e := NewEDLWriter(d, p, Hash(p))
			if e != nil {
				t.Fatal(e)
			}
			w.progress = func(p Progress) {
				if p.Stage == "source" {
					t.Error("A2 performed source range scan")
				}
			}
			if kind == "completed" {
				f, e := os.OpenFile(image, os.O_WRONLY, 0)
				if e != nil {
					t.Fatal(e)
				}
				f.WriteAt(payload, int64(rng.Offset))
				f.Close()
				w.completed = map[int]bool{0: true}
			}
			if e = w.authorize(testCtx, p, Hash(p)); e != nil {
				t.Fatal(e)
			}
			if kind == "wrong-input" {
				payload[0] ^= 1
			}
			if kind == "wrong-range" {
				rng.Offset += 512
			}
			src := payload
			if kind == "completed" {
				src = nil
			}
			e = w.Write(testCtx, rng, bytes.NewReader(src))
			if kind == "replay" {
				if e != nil {
					t.Fatal(e)
				}
				e = w.Write(testCtx, rng, bytes.NewReader(src))
			}
			good := kind == "success" || kind == "completed"
			if (e == nil) != good {
				t.Fatalf("%s: %v", kind, e)
			}
		})
	}
}
