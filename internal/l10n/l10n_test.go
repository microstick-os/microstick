package l10n

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

func TestDetect(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"unset", nil, "en"},
		{"Russian UTF-8", map[string]string{"LANG": "ru_RU.UTF-8"}, "ru"},
		{"Russian BCP47", map[string]string{"LANG": "ru-RU"}, "ru"},
		{"English region", map[string]string{"LANG": "en_GB.UTF-8"}, "en"},
		{"unsupported", map[string]string{"LANG": "de_DE.UTF-8"}, "en"},
		{"messages", map[string]string{"LANG": "en_US.UTF-8", "LC_MESSAGES": "ru_RU.UTF-8"}, "ru"},
		{"all", map[string]string{"LANG": "ru_RU.UTF-8", "LC_MESSAGES": "ru", "LC_ALL": "en_US.UTF-8"}, "en"},
		{"preference list", map[string]string{"LANG": "en_US.UTF-8", "LANGUAGE": "fr:ru:en"}, "ru"},
		{"preference English", map[string]string{"LANG": "ru_RU.UTF-8", "LANGUAGE": "en:ru"}, "en"},
		{"C ignores preference", map[string]string{"LC_ALL": "C", "LANGUAGE": "ru"}, "en"},
		{"C UTF-8", map[string]string{"LANG": "C.UTF-8", "LANGUAGE": "ru"}, "en"},
		{"POSIX", map[string]string{"LC_MESSAGES": "POSIX", "LANG": "ru"}, "en"},
		{"empty overrides", map[string]string{"LC_ALL": " ", "LANG": "ru_RU@modifier"}, "ru"},
		{"malformed", map[string]string{"LANG": "_-.@", "LANGUAGE": "::"}, "en"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(func(key string) string { return tc.env[key] })
			if got != tc.want {
				t.Fatalf("language = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCatalogs(t *testing.T) {
	if len(catalogs["en"]) != len(catalogs["ru"]) {
		t.Fatal("catalog keys differ")
	}
	for key, ru := range catalogs["ru"] {
		en, ok := catalogs["en"][key]
		if !ok || en == "" || ru == "" {
			t.Errorf("missing translation for %s", key)
		}
		if !reflect.DeepEqual(format.FindAllString(ru, -1), format.FindAllString(en, -1)) {
			t.Errorf("format arguments differ for %s", key)
		}
		if strings.ContainsFunc(en, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }) {
			t.Errorf("untranslated English message: %s", key)
		}
	}
}

func TestDiagnosticDisplayPreservesArguments(t *testing.T) {
	t.Setenv("LC_ALL", "en_US.UTF-8")
	t.Setenv("LANGUAGE", "")
	for _, tc := range []struct{ input, want string }{
		{"Запись системы", "Writing system"},
		{"Подтверждение записи загрузчика", "Verifying written bootloader"},
		{"не удалось прочитать родной modem MDT", "Could not read the device's own modem MDT"},
		{"запись прервана; reset не выполнен: недостаточно свободного места", "Writing interrupted; no reset was performed: Not enough free space"},
		{"временная файловая система не прошла проверку; журнал: /tmp/системы/журнал.log", "Temporary filesystem check failed; log: /tmp/системы/журнал.log"},
		{"EDL: readback не совпал; устройство не перезапущено. Сессия остановлена; reset не выполнен", "EDL: Readback mismatch; the device has not been restarted. Session stopped; no reset was performed"},
		{"/tmp/системы/backup", "/tmp/системы/backup"},
		{"unknown technical detail", "unknown technical detail"},
	} {
		if got := Diagnostic(tc.input); got != tc.want {
			t.Errorf("Diagnostic(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
	for _, d := range diagnostics {
		var args []any
		for _, spec := range d.formats {
			switch spec {
			case "%d":
				args = append(args, 42)
			case "%w":
				args = append(args, fmt.Errorf("original cause"))
			default:
				args = append(args, "/tmp/системы/example")
			}
		}
		input, want := d.source, d.translation
		if len(args) > 0 {
			input = fmt.Errorf(d.source, args...).Error()
			want = fmt.Errorf(d.translation, args...).Error()
		}
		if got := Diagnostic(input); got != want {
			t.Errorf("diagnostic template %q: got %q; want %q", d.source, got, want)
		}
	}
	t.Setenv("LC_ALL", "ru_RU.UTF-8")
	if got := Diagnostic("Запись системы"); got != "Запись системы" {
		t.Fatal("Russian diagnostics changed")
	}
}
