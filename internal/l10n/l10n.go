// Package l10n provides embedded installer messages and automatic locale selection.
package l10n

import (
	"embed"
	"encoding/json"
	"os"
	"regexp"
	"sort"
	"strings"
)

//go:embed locales/*.json
var resources embed.FS

var catalogs = loadCatalogs()

func loadCatalogs() map[string]map[string]string {
	result := map[string]map[string]string{}
	for _, language := range []string{"en", "ru"} {
		data, err := resources.ReadFile("locales/" + language + ".json")
		if err != nil {
			panic(err)
		}
		var messages map[string]string
		if err = json.Unmarshal(data, &messages); err != nil {
			panic(err)
		}
		result[language] = messages
	}
	return result
}

func languageTag(locale string) string {
	locale = strings.ToLower(strings.TrimSpace(locale))
	parts := strings.FieldsFunc(locale, func(r rune) bool { return strings.ContainsRune("_-.@", r) })
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// Detect follows the message locale (LC_ALL, LC_MESSAGES, LANG). GNU LANGUAGE
// supplies a preference list unless the message locale is C/POSIX.
// Unsupported languages and an unset locale fall back to English.
func Detect(getenv func(string) string) string {
	locale := ""
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if value := strings.TrimSpace(getenv(name)); value != "" {
			locale = value
			break
		}
	}
	tag := ""
	if locale != "" {
		tag = languageTag(locale)
	}
	if tag == "c" || tag == "posix" {
		return "en"
	}
	for _, preference := range strings.Split(getenv("LANGUAGE"), ":") {
		if strings.TrimSpace(preference) == "" {
			continue
		}
		if candidate := languageTag(preference); catalogs[candidate] != nil {
			return candidate
		}
	}
	if catalogs[tag] != nil {
		return tag
	}
	return "en"
}

func Language() string { return Detect(os.Getenv) }

func Text(key string) string {
	if message, ok := catalogs[Language()][key]; ok {
		return message
	}
	if message, ok := catalogs["en"][key]; ok {
		return message
	}
	return key
}

type diagnostic struct {
	source, translation string
	pattern             *regexp.Regexp
	parts, formats      []string
}

var format = regexp.MustCompile(`%[-+ #0-9.]*[a-zA-Z%]`)
var diagnostics = loadDiagnostics()

func loadDiagnostics() []diagnostic {
	var result []diagnostic
	for key, source := range catalogs["ru"] {
		if !strings.HasPrefix(key, "diagnostics.") {
			continue
		}
		d := diagnostic{source: source, translation: catalogs["en"][key]}
		d.formats = format.FindAllString(source, -1)
		if len(d.formats) > 0 {
			parts := format.Split(source, -1)
			pattern := "(?s)^" + regexp.QuoteMeta(parts[0])
			for _, part := range parts[1:] {
				pattern += "(.*?)" + regexp.QuoteMeta(part)
			}
			d.pattern = regexp.MustCompile(pattern + "$")
			d.parts = format.Split(d.translation, -1)
		}
		result = append(result, d)
	}
	sort.Slice(result, func(i, j int) bool { return len(result[i].source) > len(result[j].source) })
	return result
}

// Diagnostic translates display text without modifying errors, progress events,
// reports or persisted plans. Format arguments such as paths remain unchanged;
// wrapped errors (%w/%v) are translated recursively.
func Diagnostic(message string) string {
	if Language() == "ru" {
		return message
	}
	return translateDiagnostic(message, 0)
}

func translateDiagnostic(message string, depth int) string {
	if message == "" || depth > 32 {
		return message
	}
	for _, d := range diagnostics {
		if message == d.source {
			return d.translation
		}
		if d.pattern != nil {
			if matches := d.pattern.FindStringSubmatch(message); matches != nil {
				var out strings.Builder
				out.WriteString(d.parts[0])
				for i, value := range matches[1:] {
					if d.formats[i] == "%w" || d.formats[i] == "%v" {
						value = translateDiagnostic(value, depth+1)
					}
					out.WriteString(value)
					out.WriteString(d.parts[i+1])
				}
				return out.String()
			}
		} else if strings.HasSuffix(d.source, " ") || strings.HasSuffix(d.source, "\n") {
			if rest, ok := strings.CutPrefix(message, d.source); ok {
				return d.translation + translateDiagnostic(rest, depth+1)
			}
		} else if strings.HasPrefix(d.source, ". ") || strings.HasPrefix(d.source, "\n") {
			if rest, ok := strings.CutSuffix(message, d.source); ok {
				return translateDiagnostic(rest, depth+1) + d.translation
			}
		}
	}
	// Translate components of wrapped errors and lists of compatibility reasons.
	for _, separator := range []string{": ", "\n", "; "} {
		if parts := strings.Split(message, separator); len(parts) > 1 {
			for i := range parts {
				parts[i] = translateDiagnostic(parts[i], depth+1)
			}
			return strings.Join(parts, separator)
		}
	}
	return message
}
