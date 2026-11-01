package ui

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func webFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := fs.WalkDir(webFS, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := fs.ReadFile(webFS, p)
		out[strings.TrimPrefix(p, "web/")] = string(b)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var refRE = regexp.MustCompile(`(?i)<(?:script|link)\b[^>]*?\b(?:src|href)="([^"]+)"`)

func TestWeb_ReferencedAssetsAreEmbedded(t *testing.T) {
	files := webFiles(t)
	refs := refRE.FindAllStringSubmatch(files["index.html"], -1)
	if len(refs) == 0 {
		t.Fatal("index.html references no script or stylesheet")
	}
	for _, m := range refs {
		if _, ok := files[m[1]]; !ok {
			t.Errorf("index.html references %q, not embedded", m[1])
		}
	}
	importRE := regexp.MustCompile(`(?:from|import)\s*\(?\s*"\./([^"]+)"`)
	for name, src := range files {
		if !strings.HasSuffix(name, ".js") {
			continue
		}
		for _, m := range importRE.FindAllStringSubmatch(src, -1) {
			if _, ok := files[m[1]]; !ok {
				t.Errorf("%s imports %q, not embedded", name, m[1])
			}
		}
	}
}

func TestWeb_NoInlineOrExternalReferences(t *testing.T) {
	bad := map[string]*regexp.Regexp{
		"absolute url":    regexp.MustCompile(`(?i)(?:https?:)?//[a-z0-9.-]+\.[a-z]{2,}|\bhttps?://`),
		"inline script":   regexp.MustCompile(`(?i)<script\b(?:[^>]*\bsrc=)?[^>]*>\s*[^<\s]`),
		"style element":   regexp.MustCompile(`(?i)<style\b`),
		"style attribute": regexp.MustCompile(`(?i)\sstyle\s*=`),
		"event handler":   regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`),
		"css import/url":  regexp.MustCompile(`(?i)@import|url\(\s*["']?(?:https?:)?//`),
	}
	for name, src := range webFiles(t) {
		if strings.HasSuffix(name, ".js") {
			src = regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(src, "")
		}
		for what, re := range bad {
			if re.MatchString(src) {
				t.Errorf("%s: %s", name, what)
			}
		}
	}
}

func TestWeb_NoHTMLInjectionSinks(t *testing.T) {
	sinks := []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "createContextualFragment", "srcdoc"}
	for name, src := range webFiles(t) {
		if !strings.HasSuffix(name, ".js") {
			continue
		}
		for _, s := range sinks {
			if strings.Contains(src, s) {
				t.Errorf("%s uses %s", name, s)
			}
		}
	}
}
