package install

import (
	"os"
	"strings"
)

// detectPkgMgr picks brew on macOS when present; on Linux apt, dnf or pacman from
// os-release's ID and ID_LIKE, then whichever of them is on PATH (research D15). "" when
// there is none: the installer prints manual steps and never installs one (spec AC7).
func detectPkgMgr(e *Env) string {
	has := func(cmd string) bool { _, err := e.LookPath(cmd); return err == nil }
	if e.GOOS == "darwin" {
		if has("brew") {
			return "brew"
		}
		return ""
	}
	if e.GOOS != "linux" {
		return ""
	}
	path := e.OSRelease
	if path == "" {
		path = "/etc/os-release"
	}
	data, _ := os.ReadFile(path)
	var ids []string
	for line := range strings.SplitSeq(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && (k == "ID" || k == "ID_LIKE") {
			ids = append(ids, strings.Fields(strings.Trim(v, `"'`))...)
		}
	}
	for _, id := range ids {
		switch id {
		case "debian", "ubuntu":
			if has("apt-get") {
				return "apt"
			}
		case "fedora", "rhel", "centos":
			if has("dnf") {
				return "dnf"
			}
		case "arch":
			if has("pacman") {
				return "pacman"
			}
		}
	}
	for _, pm := range []struct{ name, cmd string }{{"apt", "apt-get"}, {"dnf", "dnf"}, {"pacman", "pacman"}} {
		if has(pm.cmd) {
			return pm.name
		}
	}
	return ""
}

// pkgCommand is the exact command shown and, on yes, run for one package. sudo appears
// here and nowhere else (spec AC5); as root it is left out.
func pkgCommand(pm, pkg string, root bool) string {
	sudo := "sudo "
	if root {
		sudo = ""
	}
	switch pm {
	case "apt":
		return sudo + "apt-get install -y " + pkg
	case "dnf":
		return sudo + "dnf install -y " + pkg
	case "pacman":
		return sudo + "pacman -S --needed --noconfirm " + pkg
	case "brew":
		return "brew install " + pkg
	}
	return ""
}
