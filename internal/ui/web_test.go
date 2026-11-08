package ui

import (
	"io/fs"
	"regexp"
	"slices"
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

// inlineOrExternalBad names what no embedded web file may hold.
var inlineOrExternalBad = map[string]*regexp.Regexp{
	"absolute url":    regexp.MustCompile(`(?i)(?:https?:)?//[a-z0-9.-]+\.[a-z]{2,}|\bhttps?://`),
	"inline script":   regexp.MustCompile(`(?i)<script\b(?:[^>]*\bsrc=)?[^>]*>\s*[^<\s]`),
	"style element":   regexp.MustCompile(`(?i)<style\b`),
	"style attribute": regexp.MustCompile(`(?i)\sstyle\s*=`),
	"event handler":   regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`),
	"css import/url":  regexp.MustCompile(`(?i)@import|url\(\s*["']?(?:https?:)?//`),
}

// jsLineComment matches a // comment. The // must not follow a colon or an opening quote,
// so the URL inside a string is left for the guard to see.
var jsLineComment = regexp.MustCompile("(?m)(^|[^:\"'`])//.*$")

// svgNamespaceLiteral is the guard's one exemption, matched whole with its quotes: the
// SVG namespace, an identifier createElementNS needs and the browser never fetches.
const svgNamespaceLiteral = `"http://www.w3.org/2000/svg"`

// inlineOrExternalFindings lists what a web file breaks. Line comments of a script are
// ignored and the SVG namespace literal is exempt, in scripts only.
func inlineOrExternalFindings(name, src string) []string {
	if strings.HasSuffix(name, ".js") {
		src = jsLineComment.ReplaceAllString(src, "$1")
		src = strings.ReplaceAll(src, svgNamespaceLiteral, `""`)
	}
	var out []string
	for what, re := range inlineOrExternalBad {
		if re.MatchString(src) {
			out = append(out, what)
		}
	}
	return out
}

func TestWeb_NoInlineOrExternalReferences(t *testing.T) {
	for name, src := range webFiles(t) {
		for _, what := range inlineOrExternalFindings(name, src) {
			t.Errorf("%s: %s", name, what)
		}
	}
}

// The guard sees URLs inside script strings, ignores them in comments, and exempts the
// SVG namespace literal exactly: nothing near it, and nowhere but a script.
func TestWeb_GuardExemptsOnlyTheSVGNamespace(t *testing.T) {
	allowed := map[string]string{
		"exact literal":    `const SVG_NS = "http://www.w3.org/2000/svg";`,
		"comment":          `const a = 1; // see https://example.com/doc`,
		"whole comment":    `// see http://example.com/doc`,
		"comment after ns": `const SVG_NS = "http://www.w3.org/2000/svg"; // https://example.com`,
	}
	for what, src := range allowed {
		if got := inlineOrExternalFindings("a.js", src); len(got) != 0 {
			t.Errorf("%s: flagged %v: %s", what, got, src)
		}
	}
	banned := map[string]string{
		"other url":         `const u = "https://evil.example.com/x.js";`,
		"http url":          `fetch("http://evil.example.com");`,
		"protocol relative": `const u = "//evil.example.com/x.js";`,
		"https namespace":   `const u = "https://www.w3.org/2000/svg";`,
		"longer path":       `const u = "http://www.w3.org/2000/svg/x.js";`,
		"prefix of path":    `const u = "http://www.w3.org/2000/sv";`,
		"other w3 path":     `const u = "http://www.w3.org/1999/xlink";`,
		"unquoted":          `fetch(http://www.w3.org/2000/svg);`,
		"after the literal": `const a = "http://www.w3.org/2000/svg", b = "https://evil.example.com";`,
		"template literal":  "const u = `http://www.w3.org/2000/svg`;",
	}
	for what, src := range banned {
		if got := inlineOrExternalFindings("a.js", src); !slices.Contains(got, "absolute url") {
			t.Errorf("%s: not flagged: %s", what, src)
		}
	}
	if got := inlineOrExternalFindings("index.html", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`); !slices.Contains(got, "absolute url") {
		t.Error("the exemption reached a file that is not a script")
	}
}

// The browser takes side calls and reminders from the API and never reads message text to
// decide (AC40). These strings are what the client rules match on, so no file of the web
// client may hold them, in any case. Test inputs may: a fixture needs real text to show
// the browser leaves it alone.
func TestWeb_NoSideCallTextMatching(t *testing.T) {
	files := webFiles(t)
	if _, ok := files["model.js"]; !ok {
		t.Fatal("the web files were not read")
	}
	for name, src := range files {
		for _, bad := range []string{"quota", "<system-reminder>", "<session>", "write the title"} {
			if strings.Contains(strings.ToLower(src), bad) {
				t.Errorf("%s contains %q", name, bad)
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

// TestWeb_ServesHarnessBadgeRendering: the tool row shows its chip and agent label and the
// hook input, the timeline renders hook lines, and the sessions list shows the hooks-only
// label. All text goes through textContent (no HTML sinks, tested above).
func TestWeb_ServesHarnessBadgeRendering(t *testing.T) {
	files := webFiles(t)
	want := map[string][]string{
		"tool-card.js":     {"chipNode(row.chip)", "row.agent", "input_differs", "hook_input", "hook_omitted_bytes"},
		"model.js":         {"agentLabel(e)", "export function chipOf"},
		"timeline-page.js": {"hookLabel(h)", "agentLabel(h)"},
		"sessions-page.js": {"hooks_only", "hooks only"},
	}
	for name, subs := range want {
		for _, sub := range subs {
			if !strings.Contains(files[name], sub) {
				t.Errorf("%s does not contain %q", name, sub)
			}
		}
	}
}

// TestWeb_ComponentsCallTheTestedHelpers: the dispatch of stream events, the omitted-bytes
// note, the hook label and the hook input's truncation live in entries.js, where the Node
// tests run them; the components must call those functions, not repeat the logic. Stream
// events reach liveEffect through liveBatch, which applies a burst of them in order.
func TestWeb_ComponentsCallTheTestedHelpers(t *testing.T) {
	files := webFiles(t)
	want := map[string][]string{
		"api.js":           {"export const STREAM_EVENTS", "for (const name of STREAM_EVENTS)"},
		"app.js":           {"liveBatch(events, st.entries)", "import { addUnique, liveBatch, upsertSession }"},
		"tool-card.js":     {"hookInputEntry(call)", "omittedNote(call.hook_omitted_bytes)"},
		"timeline-page.js": {"hookLabel(h)"},
		"entries.js":       {"export function liveEffect", "export function liveBatch", "const p = liveEffect(name, data, cur)", "export function omittedNote", "export function hookInputEntry", "export function hookLabel"},
	}
	for name, subs := range want {
		for _, sub := range subs {
			if !strings.Contains(files[name], sub) {
				t.Errorf("%s does not contain %q", name, sub)
			}
		}
	}
	for _, name := range []string{"app.js", "tool-card.js", "timeline-page.js"} {
		if strings.Contains(files[name], "replaceHooks(") || strings.Contains(files[name], "replaceExchange(") {
			t.Errorf("%s calls a replace function directly; app.js must route through liveBatch and liveEffect", name)
		}
	}
}

// TestWeb_NoCostOrContextPercent: no view shows money or a share of a context window (AC35).
func TestWeb_NoCostOrContextPercent(t *testing.T) {
	bad := regexp.MustCompile(`(?i)cost|price|percent|\d%|\}\s*%|"%|%"|[$€£]\s*\d|\$\{[^}]*\}\s*usd`)
	comment := regexp.MustCompile(`(?m)//.*$`)
	for name, src := range webFiles(t) {
		if !strings.HasSuffix(name, ".js") && name != "index.html" {
			continue
		}
		src = comment.ReplaceAllString(src, "")
		if m := bad.FindString(src); m != "" {
			t.Errorf("%s shows cost or a percentage: %q", name, m)
		}
	}
}

// TestWeb_ActionsAreButtonsOrLinks: a click handler sits only on a real <button> or <a>,
// never on a generic element dressed up with a role or an inline handler (AC33).
func TestWeb_ActionsAreButtonsOrLinks(t *testing.T) {
	files := webFiles(t)
	click := regexp.MustCompile(`(\w+)\.addEventListener\("click"`)
	for name, src := range files {
		if !strings.HasSuffix(name, ".js") {
			continue
		}
		if strings.Contains(src, `"role", "button"`) || strings.Contains(src, "onclick") {
			t.Errorf("%s fakes a button", name)
		}
		for _, m := range click.FindAllStringSubmatch(src, -1) {
			decl := regexp.MustCompile(`(?:const|let)\s+` + m[1] + `\s*=\s*(?:document\.createElement\("(?:button|a)"\)|el\("(?:button|a)")`)
			if !decl.MatchString(src) {
				t.Errorf("%s: click handler on %s, which is not a button or link", name, m[1])
			}
		}
	}
	for _, name := range []string{"shell.js", "tool-card.js", "sessions-page.js"} {
		if !strings.Contains(files[name], `addEventListener("click"`) {
			t.Errorf("%s: expected a click handler on a button", name)
		}
	}
}
