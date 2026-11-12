package firmware

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reviewedProfiles(t *testing.T) []map[string]any {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(external, "board/msm8916/operator-profiles.json"))
	if e != nil {
		t.Fatal(e)
	}
	var profiles []map[string]any
	if e = json.Unmarshal(raw, &profiles); e != nil {
		t.Fatal(e)
	}
	var out []map[string]any
	for _, p := range profiles {
		for _, operator := range p["operators"].([]any) {
			copy := object(t, js(p))
			delete(copy, "operators")
			copy["operator"] = operator
			out = append(out, copy)
		}
	}
	return out
}
func TestAPNResolver(t *testing.T) {
	binary := compile(t, "apn", []string{source("apn.c"), filepath.Join(external, "tests/apn_test.c")}, []string{"glib-2.0"}, nil)
	catalog := reviewedProfiles(t)
	catalog = append(catalog, map[string]any{"id": "old-yota", "operator": "25011", "apn": "yota.ru"}, map[string]any{"id": "old-beeline", "operator": "25099", "apn": "home.beeline.ru"}, map[string]any{"id": "gb", "name": "International", "operator": "23415", "apn": "international"}, map[string]any{"id": "amb1", "operator": "12345", "apn": "first"}, map[string]any{"id": "amb2", "operator": "12345", "apn": "second"})
	resolve := func(t *testing.T, home, spn, gid string, config map[string]any) map[string]any {
		return object(t, ok(t, nativeEnv(nil), js(map[string]any{"catalog": catalog, "config": config, "home": home, "spn": spn, "gid1": gid})+"\n", binary))
	}
	for _, v := range []struct{ name, home, spn, gid, apn string }{
		{"mts", "25001", "", "", "internet.mts.ru"}, {"megafon", "25002", "", "", "internet"}, {"beeline", "25099", "", "", "internet.beeline.ru"}, {"t2", "25020", "Tele2", "", "internet"}, {"yota", "25011", "", "", "internet.yota"}, {"tbank", "25062", "", "", "tt"}, {"tbank_shared", "25020", "Tinkoff Mobile", "", "tt"}, {"sber", "25050", "", "", "sberbank"}, {"sber_gid", "25020", "", "fa3207ff00", "sberbank"}, {"sber_spn", "25020", "СберМобайл", "", "sberbank"}, {"vtb", "25026", "", "", "vtb"}, {"gpb", "25020", "GPB Mobile", "", "gpb"}, {"rostelecom", "25020", "Rostelecom", "", "internet.rtk.ru"}, {"international", "23415", "", "", "international"},
	} {
		t.Run(v.name, func(t *testing.T) {
			r := resolve(t, v.home, v.spn, v.gid, map[string]any{"profile": "auto"})
			equal(t, r["ok"], true)
			equal(t, r["apn"], v.apn)
			equal(t, len(r["profiles"].([]any)), 10)
		})
	}
	for _, v := range []struct{ home, state string }{{"", "pending"}, {"99999", "unknown"}, {"12345", "ambiguous"}, {"25020", "unknown"}} {
		r := resolve(t, v.home, "", "", map[string]any{})
		equal(t, r["ok"], false)
		equal(t, r["state"], v.state)
	}
	// An explicitly selected MVNO needs no SIM metadata. Manual settings and
	// credentials take precedence; switching back to auto follows the new SIM.
	r := resolve(t, "25020", "T2", "", map[string]any{"profile": "ru-gpbmobile"})
	equal(t, r["apn"], "gpb")
	r = resolve(t, "25011", "Yota", "", map[string]any{"apn": "private.example", "password": "PRIVATE-MANUAL"})
	equal(t, r["apn"], "private.example")
	equal(t, r["profile"], "manual")
	equal(t, r["password"], "PRIVATE-MANUAL")
	equal(t, resolve(t, "25011", "", "", map[string]any{"profile": "auto"})["apn"], "internet.yota")
	equal(t, resolve(t, "25099", "", "", map[string]any{"profile": "auto"})["apn"], "internet.beeline.ru")
	equal(t, resolve(t, "25011", "", "", map[string]any{"profile": "missing"})["ok"], false)
}

func TestAPNActions(t *testing.T) {
	f := newFixture(t)
	catalog := filepath.Join(f.root, "providers.json")
	write(t, catalog, js(reviewedProfiles(t)))
	f.env["MICROSTICK_PROVIDERS"] = catalog
	saved := `{"apn":"private.example","username":"CUSTOM-USER","password":"CUSTOM-PASSWORD","auth":"pap"}`
	write(t, filepath.Join(f.e, "lte.json"), saved)
	if _, e := f.action("lte", map[string]any{"enabled": true}); e != nil {
		t.Fatal(e)
	}
	equal(t, read(t, filepath.Join(f.e, "lte.json")), saved)
	if _, e := f.action("lte", map[string]any{"profile": "manual", "apn": "new.example"}); e != nil {
		t.Fatal(e)
	}
	manual := object(t, read(t, filepath.Join(f.e, "lte.json")))
	equal(t, manual["password"], "CUSTOM-PASSWORD")
	equal(t, manual["auth"], "pap")
	for _, profile := range []string{"auto", "ru-yota", "ru-gpbmobile"} {
		if _, e := f.action("lte", map[string]any{"profile": profile}); e != nil {
			t.Fatal(e)
		}
		r := read(t, filepath.Join(f.e, "lte.json"))
		equal(t, object(t, r)["profile"], profile)
		no(t, strings.Contains(r, "CUSTOM-"), "manual credentials retained in automatic config")
	}
	previous := read(t, filepath.Join(f.e, "lte.json"))
	_, e := f.action("lte", map[string]any{"profile": "not-in-catalog"})
	no(t, e == nil, "unknown profile accepted")
	equal(t, read(t, filepath.Join(f.e, "lte.json")), previous)
	if e = os.Remove(filepath.Join(f.e, "lte.json")); e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(f.e, "lte.properties"), "apn=legacy.example,user=LEGACY-USER,password=LEGACY-PASSWORD,allowed-auth=pap\n")
	if _, e = f.action("lte", map[string]any{"profile": "manual", "apn": "new.example"}); e != nil {
		t.Fatal(e)
	}
	manual = object(t, read(t, filepath.Join(f.e, "lte.json")))
	equal(t, manual["username"], "LEGACY-USER")
	equal(t, manual["password"], "LEGACY-PASSWORD")
	equal(t, strings.TrimSpace(manual["auth"].(string)), "pap")
}

func TestAPNTransport(t *testing.T) {
	binary := compile(t, "apn", []string{source("apn.c"), filepath.Join(external, "tests/apn_test.c")}, []string{"glib-2.0"}, nil)
	f := newFixture(t)
	f.modem("registered", "none", false)
	catalog := filepath.Join(f.root, "providers.json")
	write(t, catalog, js(reviewedProfiles(t)))
	write(t, filepath.Join(f.e, "lte.properties"), "# automatic\n")
	helper := filepath.Join(f.root, "modem-helper")
	write(t, helper, `#!/bin/sh
case "$1" in
 profile) config=$(cat); jq -cn --argjson config "$config" --slurpfile catalog "$MICROSTICK_PROVIDERS" --arg home "$TEST_HOME" --arg spn "$TEST_SPN" '{config:$config,catalog:$catalog[0],home:$home,spn:$spn}' | "$TEST_APN_RESOLVER" ;;
 connect) cat > "$MICROSTICK_RUN/connected-private.json"; printf '{"ok":true}\n' ;;
 *) exit 2 ;;
esac
`)
	f.env["MICROSTICK_MODEM"] = helper
	f.env["MICROSTICK_PROVIDERS"] = catalog
	f.env["TEST_APN_RESOLVER"] = binary
	f.env = nativeEnv(f.env)
	for _, v := range []struct{ home, spn, apn string }{{"25011", "Yota", "internet.yota"}, {"25099", "Beeline", "internet.beeline.ru"}, {"25020", "GPB Mobile", "gpb"}, {"25020", "T2", "internet"}} {
		f.env["TEST_HOME"] = v.home
		f.env["TEST_SPN"] = v.spn
		if _, e := f.shell("poll_lte"); e != nil {
			t.Fatal(e)
		}
		equal(t, object(t, read(t, filepath.Join(f.r, "connected-private.json")))["apn"], v.apn)
		public := read(t, filepath.Join(f.r, "lte-profile.json"))
		equal(t, object(t, public)["effective_apn"], v.apn)
		no(t, strings.Contains(public, "password") || strings.Contains(public, "username"), "credentials published")
		no(t, exists(filepath.Join(f.r, "lte-resolved-private.json")), "temporary connection credentials retained")
	}
	f.env["TEST_HOME"] = "25020"
	f.env["TEST_SPN"] = ""
	_, e := f.shell("poll_lte")
	no(t, e == nil, "unidentified shared-network SIM silently used host APN")
	equal(t, strings.TrimSpace(read(t, filepath.Join(f.r, "lte-error"))), "apn_required")
	f.env["TEST_HOME"] = ""
	_, e = f.shell("poll_lte")
	no(t, e == nil, "missing SIM metadata accepted")
	equal(t, strings.TrimSpace(read(t, filepath.Join(f.r, "lte-error"))), "apn_pending")
	// Applying a different profile replaces an already connected bearer once.
	if _, e = f.action("lte", map[string]any{"profile": "ru-yota"}); e != nil {
		t.Fatal(e)
	}
	f.modem("connected", "none", true)
	f.env["TEST_HOME"] = "25011"
	f.env["TEST_SPN"] = "Yota"
	if _, e = f.shell("poll_lte"); e != nil {
		t.Fatal(e)
	}
	no(t, !f.hasCall("mmcli", "-m", "/Modem/0", "--simple-disconnect"), "new profile kept old active bearer")
	no(t, exists(filepath.Join(f.r, "lte-profile-changed")), "profile reconnect marker retained")
}

func TestTargetAPNProfiles(t *testing.T) {
	catalog := filepath.Join(t.TempDir(), "providers.json")
	write(t, catalog, js(reviewedProfiles(t)))
	e := map[string]string{"MICROSTICK_PROVIDERS": catalog, "DBUS_SYSTEM_BUS_ADDRESS": "unix:path=" + filepath.Join(t.TempDir(), "no-system-bus")}
	binary := filepath.Join(workspace, "buildroot-output/target/usr/libexec/microstick-modem")
	qemu := filepath.Join(workspace, "buildroot-output/host/bin/qemu-aarch64")
	for _, v := range []struct{ profile, apn string }{{"ru-yota", "internet.yota"}, {"ru-tbank", "tt"}, {"ru-sbermobile", "sberbank"}, {"ru-gpbmobile", "gpb"}} {
		raw := ok(t, e, js(map[string]any{"profile": v.profile})+"\n", qemu, append(qemuArgs(binary), "profile")...)
		r := object(t, raw)
		equal(t, r["ok"], true)
		equal(t, r["apn"], v.apn)
	}
	raw, err := run(t, e, "{\"profile\":\"auto\"}\n", qemu, append(qemuArgs(binary), "profile")...)
	no(t, err == nil, "target helper invented a SIM without ModemManager")
	equal(t, object(t, raw)["error"], "apn_pending")
}
