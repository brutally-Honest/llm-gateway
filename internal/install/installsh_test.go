package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// installSh is the repo's install.sh, tested from here because it has no Go package.
var installSh, _ = filepath.Abs(filepath.Join("..", "..", "install.sh"))

// install.sh is shellcheck clean (spec AC23). shellcheck is a make verify requirement
// (ADR 0011), so it is not skipped when missing.
func TestInstallShShellcheck(t *testing.T) {
	sc, err := exec.LookPath("shellcheck")
	if err != nil {
		t.Fatal("shellcheck not found: install it (ADR 0011)")
	}
	if out, err := exec.Command(sc, installSh).CombinedOutput(); err != nil {
		t.Fatalf("shellcheck: %v\n%s", err, out)
	}
}

// release is a local server holding SHA256SUMS and one archive for this machine, built
// here: its gateway is a script that records how it was called.
type release struct {
	srv   *httptest.Server
	hits  atomic.Int32
	file  string
	sums  atomic.Pointer[string] // swapped by a test while the server runs
	osArc string
}

func (r *release) setSums(s string) { r.sums.Store(&s) }

const fakeGateway = "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" > \"$HOME/gateway-ran\"\n[ -t 0 ] && echo tty >> \"$HOME/gateway-ran\"\nexit 0\n"

func newRelease(t *testing.T, corrupt bool) *release {
	t.Helper()
	goarch := runtime.GOARCH
	r := &release{osArc: runtime.GOOS + "_" + goarch, file: "llm-gateway_9.9.9_" + runtime.GOOS + "_" + goarch + ".tar.gz"}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "gateway", Mode: 0o755, Size: int64(len(fakeGateway))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(fakeGateway)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	hexsum := hex.EncodeToString(sum[:])
	if corrupt {
		hexsum = strings.Repeat("0", 64)
	}
	r.setSums(fmt.Sprintf("%s  llm-gateway_9.9.9_other_arch.tar.gz\n%s  %s\n", strings.Repeat("1", 64), hexsum, r.file))
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.hits.Add(1)
		switch req.URL.Path {
		case "/rel/SHA256SUMS":
			_, _ = w.Write([]byte(*r.sums.Load()))
		case "/rel/" + r.file:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// restrictedPath is a folder of links to the tools install.sh uses, minus drop, plus
// extra scripts (a fake uname).
func restrictedPath(t *testing.T, drop []string, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	tools := []string{"sh", "uname", "curl", "tar", "sha256sum", "shasum", "mktemp", "grep", "head", "cut",
		"mkdir", "chmod", "rm", "cat", "gzip", "printf"}
	for _, tool := range tools {
		if slices.Contains(drop, tool) {
			continue
		}
		if p, err := exec.LookPath(tool); err == nil {
			if err := os.Symlink(p, filepath.Join(dir, tool)); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, body := range extra {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func runInstallSh(t *testing.T, r *release, path string, args ...string) (string, string, error) {
	t.Helper()
	home := t.TempDir()
	cmd := exec.Command("/bin/sh", append([]string{installSh}, args...)...)
	cmd.Env = []string{"HOME=" + home, "PATH=" + path, "LLM_GATEWAY_BASE_URL=" + r.srv.URL + "/rel/"}
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()
	return home, string(out), err
}

func TestInstallShGoodChecksumHandsOver(t *testing.T) {
	r := newRelease(t, false)
	home, out, err := runInstallSh(t, r, restrictedPath(t, nil, nil), "v9.9.9", "--check", "--install-deps")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"✓ OS and CPU", "✓ curl or wget", "✓ tar", "✓ checksum tool", "Checksum ok"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	ran, err := os.ReadFile(filepath.Join(home, "gateway-ran"))
	if err != nil {
		t.Fatalf("gateway not run:\n%s", out)
	}
	// Flags pass through; the version is not one of them.
	if !strings.HasSuffix(strings.TrimSpace(strings.SplitN(string(ran), "\n", 2)[0]), "gateway install --check --install-deps") {
		t.Fatalf("gateway ran as %q", ran)
	}
	// The script itself writes nothing in HOME: gateway install copies the binary.
	entries, _ := os.ReadDir(home)
	if len(entries) != 1 {
		t.Fatalf("home holds %v", entries)
	}
}

func TestInstallShBadChecksumStops(t *testing.T) {
	r := newRelease(t, true)
	home, out, err := runInstallSh(t, r, restrictedPath(t, nil, nil))
	if err == nil || !strings.Contains(out, "checksum mismatch") || !strings.Contains(out, "nothing was installed") {
		t.Fatalf("err %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, "gateway-ran")); err == nil {
		t.Fatal("gateway ran after a bad checksum")
	}
}

func TestInstallShUnsupportedArchStops(t *testing.T) {
	r := newRelease(t, false)
	uname := "#!/bin/sh\ncase \"$1\" in -m) echo riscv64 ;; *) echo Linux ;; esac\n"
	_, out, err := runInstallSh(t, r, restrictedPath(t, []string{"uname"}, map[string]string{"uname": uname}))
	if err == nil || !strings.Contains(out, "✗ OS and CPU") || !strings.Contains(out, "Linux/riscv64") {
		t.Fatalf("err %v\n%s", err, out)
	}
	if r.hits.Load() != 0 {
		t.Fatal("downloaded before stopping")
	}
}

// install.sh gives the hint for the OS it runs on; uname is faked so both are checked on
// any machine (spec 024 AC6, macos-v1.md B6).
func TestInstallShMissingTarStops(t *testing.T) {
	for osName, hint := range map[string]string{
		"Linux":  "install the tar package",
		"Darwin": "install it (with Homebrew: brew install tar)",
	} {
		t.Run(osName, func(t *testing.T) {
			r := newRelease(t, false)
			uname := "#!/bin/sh\ncase \"$1\" in -m) echo x86_64 ;; *) echo " + osName + " ;; esac\n"
			_, out, err := runInstallSh(t, r, restrictedPath(t, []string{"tar", "uname"}, map[string]string{"uname": uname}))
			if err == nil || !strings.Contains(out, "✗ tar") || !strings.Contains(out, hint) {
				t.Fatalf("err %v, want the hint %q\n%s", err, hint, out)
			}
			if !strings.Contains(out, "nothing was downloaded") || r.hits.Load() != 0 {
				t.Fatalf("downloaded with tar missing:\n%s", out)
			}
		})
	}
}

func TestInstallShNoBuildForPlatform(t *testing.T) {
	r := newRelease(t, false)
	r.setSums(strings.Repeat("1", 64) + "  llm-gateway_9.9.9_other_arch.tar.gz\n")
	_, out, err := runInstallSh(t, r, restrictedPath(t, nil, nil))
	if err == nil || !strings.Contains(out, "this release has no build for") {
		t.Fatalf("err %v\n%s", err, out)
	}
}

// B10 (spec 024 AC7): with --check, install.sh says the archive it fetched for the check
// was removed, and leaves no temp folder behind; without it, it says nothing of the kind.
func TestInstallShCheckSaysArchiveRemoved(t *testing.T) {
	r := newRelease(t, false)
	tmp := t.TempDir()
	path := restrictedPath(t, nil, nil)
	cmd := exec.Command("/bin/sh", installSh, "--check")
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + path, "TMPDIR=" + tmp, "LLM_GATEWAY_BASE_URL=" + r.srv.URL + "/rel/"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "The archive was downloaded to a temporary folder for the check and removed.") {
		t.Fatalf("output lacks the archive line:\n%s", out)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Fatalf("temp folder left behind: %v", left)
	}
	_, out2, err := runInstallSh(t, r, restrictedPath(t, nil, nil), "--yes")
	if err != nil || strings.Contains(out2, "for the check") {
		t.Fatalf("without --check: %v\n%s", err, out2)
	}
}
