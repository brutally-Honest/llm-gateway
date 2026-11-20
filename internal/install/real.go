package install

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/brutally-honest/llm-gateway/internal/config"
)

// execRunner runs real commands.
type execRunner struct{}

func (execRunner) Output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// Shell runs a command the user was shown and said yes to, with the terminal attached so
// sudo can ask for a password.
func (execRunner) Shell(line string, out io.Writer) error {
	cmd := exec.Command("sh", "-c", line)
	cmd.Stdout, cmd.Stderr = out, out
	if tty, err := os.Open("/dev/tty"); err == nil {
		defer func() { _ = tty.Close() }()
		cmd.Stdin = tty
	}
	return cmd.Run()
}

// RealEnv is the machine the command runs on.
func RealEnv(version string, in io.Reader, out io.Writer, loadConfig func(string) (config.Config, error)) *Env {
	home, _ := os.UserHomeDir()
	name := os.Getenv("USER")
	uid := os.Getuid()
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	exe, _ := os.Executable()
	e := &Env{
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Home: home, User: name, UID: uid, Root: uid == 0,
		Getenv:     os.Getenv,
		LookPath:   exec.LookPath,
		Run:        execRunner{},
		HTTPStatus: httpStatus,
		Dial: func(addr string) bool {
			c, err := net.DialTimeout("tcp", addr, time.Second)
			if err == nil {
				_ = c.Close()
			}
			return err == nil
		},
		Fetch:       fetchBody,
		Now:         time.Now,
		FreeBytes:   freeBytes,
		In:          in,
		Out:         out,
		Interactive: isTerminal(in),
		Exe:         exe,
		Version:     version,
		LoadConfig:  loadConfig,
		Sleep:       time.Sleep,
	}
	e.Ports = func(port int) (bool, string) { return probePort(e, port) }
	return e
}

func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func httpStatus(url string) (int, error) {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// fetchBody GETs a loopback URL and returns its body, up to 4 MiB.
func fetchBody(url string) (string, error) {
	c := http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return string(body), err
}

// probePort binds the port for a moment to see if it is free; a busy port's holder is
// named with lsof or ss when either is there (research D13).
func probePort(e *Env, port int) (bool, string) {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err == nil {
		_ = l.Close()
		return true, ""
	}
	return false, portHolder(e, port)
}

var ssUser = regexp.MustCompile(`users:\(\("([^"]+)",pid=(\d+)`)

func portHolder(e *Env, port int) string {
	if _, err := e.LookPath("lsof"); err == nil {
		out, _ := e.Run.Output("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc")
		var pid, cmd string
		for line := range strings.SplitSeq(out, "\n") {
			switch {
			case strings.HasPrefix(line, "p") && pid == "":
				pid = line[1:]
			case strings.HasPrefix(line, "c") && cmd == "":
				cmd = line[1:]
			}
		}
		if cmd != "" {
			return fmt.Sprintf("%s (pid %s)", cmd, pid)
		}
	}
	if _, err := e.LookPath("ss"); err == nil {
		out, _ := e.Run.Output("ss", "-Hltnp", "sport = :"+strconv.Itoa(port))
		if m := ssUser.FindStringSubmatch(out); m != nil {
			return fmt.Sprintf("%s (pid %s)", m[1], m[2])
		}
	}
	return ""
}
