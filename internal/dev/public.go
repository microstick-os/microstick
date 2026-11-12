package dev

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"image/png"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func PublicFiles(repo string) ([]string, error) {
	top := map[string]bool{"README.md": true, "README.ru.md": true, ".gitignore": true, "extract-blobs": true, "check-public": true, "prepare-install": true, "devtool": true, "flasher": true, "go.mod": true, "go.sum": true}
	dirs := map[string]bool{"buildroot-external": true, "docs": true, "cmd": true, "internal": true, "scripts": true}
	extensions := map[string]bool{"": true, ".md": true, ".go": true, ".c": true, ".h": true, ".mk": true, ".in": true, ".desc": true, ".patch": true, ".hash": true, ".conf": true, ".fragment": true, ".properties": true, ".service": true, ".sh": true, ".jq": true, ".json": true, ".html": true, ".js": true, ".css": true, ".svg": true, ".rules": true}
	private := map[string]bool{"credentials.json": true, "sessions.json": true, "default-auth.json": true, "default-imei.json": true, "MANIFEST.json": true, "SHA256SUMS": true, "auth.json": true}
	key := regexp.MustCompile(`(?m)^-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)
	forbiddenInterpreter := regexp.MustCompile(`(?m)(?:^|[\s/])python(?:[0-9]+(?:\.[0-9]+)?)?(?:\s|$)`)
	var names, problems []string
	err := filepath.WalkDir(repo, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if path == repo {
			return nil
		}
		rel, e := filepath.Rel(repo, path)
		if e != nil {
			return e
		}
		parts := strings.Split(rel, string(os.PathSeparator))
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			problems = append(problems, rel+": symlink")
			return nil
		}
		if d.IsDir() {
			if !dirs[parts[0]] {
				problems = append(problems, rel+": unexpected directory")
			}
			return nil
		}
		allowed := (len(parts) == 1 && top[d.Name()]) || (len(parts) > 1 && dirs[parts[0]] && extensions[filepath.Ext(path)])
		allowed = allowed || (strings.HasPrefix(filepath.ToSlash(rel), "internal/dev/testdata/firehose/") && filepath.Ext(path) == ".xml")
		documentationPNG := filepath.ToSlash(rel) == "docs/images/desktop_en.png" || filepath.ToSlash(rel) == "docs/images/desktop_ru.png"
		allowed = allowed || documentationPNG
		if !allowed || private[d.Name()] || strings.Contains(rel, string(os.PathSeparator)+".state"+string(os.PathSeparator)) {
			problems = append(problems, rel+": artifact/private/unexpected file")
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if documentationPNG {
			if _, e := png.Decode(bytes.NewReader(data)); e != nil {
				problems = append(problems, rel+": invalid documentation PNG")
				return nil
			}
		}
		if (!documentationPNG && (!utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0)) || key.Match(data) {
			problems = append(problems, rel+": binary/private data")
			return nil
		}
		if bytes.HasPrefix(data, []byte("#!")) && forbiddenInterpreter.Match(data) {
			problems = append(problems, rel+": forbidden interpreter invocation")
			return nil
		}
		names = append(names, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	sort.Strings(names)
	return names, nil
}
func ArchiveSources(p Paths, epoch int64) error {
	names, e := PublicFiles(p.Repo)
	if e != nil {
		return e
	}
	f, e := os.Create(filepath.Join(p.Artifacts, "microstick-source.tar.gz"))
	if e != nil {
		return e
	}
	gz := gzip.NewWriter(f)
	gz.Header.ModTime = time.Unix(epoch, 0)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		path := filepath.Join(p.Repo, name)
		st, e := os.Stat(path)
		if e != nil {
			return e
		}
		h, e := tar.FileInfoHeader(st, "")
		if e != nil {
			return e
		}
		h.Name = "microstick/" + name
		h.ModTime = time.Unix(epoch, 0)
		h.Uid = 0
		h.Gid = 0
		h.Uname = "root"
		h.Gname = "root"
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		src, e := os.Open(path)
		if e != nil {
			return e
		}
		_, e = io.Copy(tw, src)
		src.Close()
		if e != nil {
			return e
		}
	}
	if e = tw.Close(); e != nil {
		return e
	}
	if e = gz.Close(); e != nil {
		return e
	}
	return f.Close()
}
func CheckPublic(p Paths) error {
	names, e := PublicFiles(p.Repo)
	if e != nil {
		return e
	}
	fmt.Printf("PASS: %d public source files; no blobs, generated artifacts, external symlinks or disallowed languages\n", len(names))
	return nil
}
