package firmware

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fault injection executes the production shell, redirecting storage discovery
// and mounting to private fixtures. It never opens real devices.
func TestFirstBootFormatPreserveAndPowerLoss(t *testing.T) {
	for _, scenario := range []string{"clean", "mkfs-failure", "after-mkfs", "mount-failure", "seed-failure", "preserve-failure", "wrong-geometry", "wrong-partuuid", "bad-state"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			conf := filepath.Join(dir, "uuid")
			contract := filepath.Join(dir, "storage.json")
			state := filepath.Join(dir, "phase")
			mounts := filepath.Join(dir, "mounts")
			volume := filepath.Join(dir, "volume-uuid")
			calls := filepath.Join(dir, "formats")
			script := filepath.Join(dir, "microstick-data")
			size := filepath.Join(dir, "size")
			const uuid = "01234567-89ab-cdef-0123-456789abcdef"
			const fsUUID = "11234567-89ab-cdef-0123-456789abcdef"
			const id = "0123456789abcdef0123456789abcdef"
			write(t, conf, uuid+"\n")
			write(t, mounts, "")
			write(t, size, "65536\n")
			mode := "format"
			if scenario == "preserve-failure" {
				mode = "preserve"
			}
			values := map[string]any{"schema_version": 1, "installation": id, "mode": mode, "partuuid": uuid, "bytes": 32 << 20, "fs_uuid": fsUUID}
			if scenario == "wrong-partuuid" {
				values["partuuid"] = "another"
			}
			b, _ := json.Marshal(values)
			write(t, contract, string(b))
			if scenario == "wrong-geometry" {
				write(t, size, "123\n")
			}
			if scenario == "bad-state" {
				write(t, state, id+" invalid\n")
			}
			write(t, filepath.Join(bin, "blkid"), "#!/bin/sh\ncase \"$*\" in *PARTUUID*) echo /dev/fixture-data;; *) cat '"+volume+"' 2>/dev/null;; esac\n")
			write(t, filepath.Join(bin, "mke2fs"), "#!/bin/sh\necho format >> '"+calls+"'\n[ ! -e '"+dir+"/fail-mkfs' ] || exit 1\nprintf '%s\\n' '"+fsUUID+"' > '"+volume+"'\n")
			write(t, filepath.Join(bin, "e2fsck"), "#!/bin/sh\n[ ! -e '"+dir+"/fail-fsck' ]\n")
			// exit 4 is an uncorrectable check, preventing a formatting checkpoint.
			write(t, filepath.Join(bin, "e2fsck"), "#!/bin/sh\n[ ! -e '"+dir+"/fail-fsck' ] || exit 4\n")
			write(t, filepath.Join(bin, "mount"), "#!/bin/sh\n[ ! -e '"+dir+"/fail-mount' ] || exit 1\nprintf '%s\\n' '/dev/fixture-data /data ext4 rw,nodev,nosuid 0 0' > '"+mounts+"'\n")
			write(t, filepath.Join(bin, "sync"), "#!/bin/sh\nexit 0\n")
			update := filepath.Join(bin, "data-update")
			write(t, update, "#!/bin/sh\n[ ! -e '"+dir+"/fail-seed' ] || exit 1\necho seeded > '"+dir+"/seeded'\n")
			s := read(t, overlay("usr/libexec/microstick-data"))
			s = strings.ReplaceAll(s, "/etc/microstick/data-partuuid", conf)
			s = strings.ReplaceAll(s, "/etc/microstick/storage.json", contract)
			s = strings.ReplaceAll(s, "/var/lib/microstick/first-boot", state)
			s = strings.ReplaceAll(s, "/proc/mounts", mounts)
			s = strings.ReplaceAll(s, `"/sys/class/block/$block/size"`, "'"+size+"'")
			s = strings.ReplaceAll(s, `[ -b "$wanted" ]`, ":")
			s = strings.ReplaceAll(s, "/usr/libexec/microstick-data-update", update)
			write(t, script, s)
			env := map[string]string{"PATH": bin + ":" + os.Getenv("PATH")}
			switch scenario {
			case "mkfs-failure":
				write(t, filepath.Join(dir, "fail-mkfs"), "")
			case "after-mkfs":
				write(t, filepath.Join(dir, "fail-fsck"), "")
			case "mount-failure", "preserve-failure":
				write(t, filepath.Join(dir, "fail-mount"), "")
			case "seed-failure":
				write(t, filepath.Join(dir, "fail-seed"), "")
			}
			_, e := run(t, env, "", script, "start")
			if (e == nil) != (scenario == "clean") {
				t.Fatalf("unexpected first result %v", e)
			}
			if scenario == "wrong-geometry" || scenario == "wrong-partuuid" || scenario == "bad-state" || scenario == "preserve-failure" {
				if _, e := os.Stat(calls); !os.IsNotExist(e) {
					t.Fatal("formatted without permission")
				}
				if _, e := os.Stat(filepath.Join(dir, "seeded")); !os.IsNotExist(e) {
					t.Fatal("seed ran after storage error")
				}
				return
			}
			// Reboot clears mounts but keeps durable state and the actual filesystem UUID.
			for _, f := range []string{"fail-mkfs", "fail-fsck", "fail-mount", "fail-seed"} {
				os.Remove(filepath.Join(dir, f))
			}
			write(t, mounts, "")
			ok(t, env, "", script, "start")
			write(t, mounts, "")
			ok(t, env, "", script, "start")
			formats := strings.Count(read(t, calls), "format\n")
			want := 1
			if scenario == "mkfs-failure" {
				want = 2
			}
			if formats != want {
				t.Fatalf("formatted %d times, want %d", formats, want)
			}
			if read(t, state) != id+" done\n" {
				t.Fatal("first boot not completed")
			}
		})
	}
}

func TestDataUpdateKeepsPluginStateAndDeletedDefaults(t *testing.T) {
	for _, phase := range []string{"normal", "old-moved", "next-moved"} {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			volume := filepath.Join(dir, "volume")
			incoming := filepath.Join(dir, "incoming")
			gate := filepath.Join(dir, "gate")
			contract := filepath.Join(dir, "storage.json")
			removed := filepath.Join(dir, "removed")
			script := filepath.Join(dir, "update")
			bin := filepath.Join(dir, "bin")
			const id = "0123456789abcdef0123456789abcdef"
			write(t, gate, "#!/bin/sh\nexit 0\n")
			write(t, contract, `{"transaction":"`+id+`"}`)
			write(t, removed, "obsolete\n")
			write(t, filepath.Join(bin, "sync"), "#!/bin/sh\nexit 0\n")
			current := filepath.Join(volume, "plugins/web")
			txn := filepath.Join(volume, ".microstick-update-"+id+"-web")
			write(t, filepath.Join(current, "run"), "old-code\n")
			write(t, filepath.Join(current, ".state/history"), "retained-user-data\n")
			write(t, filepath.Join(incoming, "web/run"), "new-code\n")
			if phase != "normal" {
				if e := os.MkdirAll(txn, 0700); e != nil {
					t.Fatal(e)
				}
				if e := os.Rename(current, filepath.Join(txn, "old")); e != nil {
					t.Fatal(e)
				}
				write(t, filepath.Join(txn, "next/run"), "new-code\n")
				write(t, filepath.Join(txn, "next/.state/history"), "retained-user-data\n")
				if phase == "next-moved" {
					if e := os.Rename(filepath.Join(txn, "next"), current); e != nil {
						t.Fatal(e)
					}
				}
			}
			s := read(t, overlay("usr/libexec/microstick-data-update"))
			s = strings.NewReplacer("/usr/libexec/microstick-data", gate, "/usr/share/microstick/data-update/plugins", incoming, "/etc/microstick/storage.json", contract, "/etc/microstick/deleted-default-plugins", removed, "/etc/microstick/data-auth.json", filepath.Join(dir, "absent-auth"), "/data", volume).Replace(s)
			write(t, script, s)
			env := map[string]string{"PATH": bin + ":" + os.Getenv("PATH")}
			ok(t, env, "", script)
			ok(t, env, "", script)
			equal(t, read(t, filepath.Join(current, "run")), "new-code\n")
			equal(t, read(t, filepath.Join(current, ".state/history")), "retained-user-data\n")
			if _, e := os.Stat(filepath.Join(volume, ".default-plugins/obsolete")); e != nil {
				t.Fatal("deleted default would be reseeded", e)
			}
		})
	}
}
