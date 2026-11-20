package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
)

// State is what install made, kept in install-state.json for uninstall and doctor
// (research D9). It holds no time, so installing twice writes the same bytes (AC17).
type State struct {
	Version string `json:"version"`
	// Files are the files install wrote and owns, the binary and config excluded.
	Files []string `json:"files"`
	// Backups maps a file install replaced to the .bak it kept of the original.
	Backups map[string]string `json:"backups,omitempty"`
	// ConfigCreated says install wrote the config (it is still kept on uninstall).
	ConfigCreated bool          `json:"config_created,omitempty"`
	Shell         *ShellState   `json:"shell,omitempty"`
	Service       *ServiceState `json:"service,omitempty"`
}

// ShellState is the startup file install put its block in.
type ShellState struct {
	File string `json:"file"`
	// Replaced is the hand-written function the block took the place of, put back on
	// uninstall (research D10).
	Replaced string `json:"replaced,omitempty"`
	// Created says the file did not exist before install.
	Created bool `json:"created,omitempty"`
}

// ServiceState is the login service install set up.
type ServiceState struct {
	Kind string `json:"kind"`
	File string `json:"file"`
	// Prior is the state of the user's own service file install replaced, put back on
	// uninstall (research D25); nil when install replaced none.
	Prior *PriorService `json:"prior,omitempty"`
}

// PriorService is whether a replaced service was enabled at login and running.
type PriorService struct {
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
}

func readState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func (s *State) encode() []byte {
	slices.Sort(s.Files)
	s.Files = slices.Compact(s.Files)
	out, _ := json.MarshalIndent(s, "", "  ")
	return append(out, '\n')
}

func (s *State) owns(path string) bool {
	return s != nil && slices.Contains(s.Files, path)
}

func (s *State) backup(path string) string {
	if s == nil {
		return ""
	}
	return s.Backups[path]
}

// readMaybe reads path; ok is false when it does not exist.
func readMaybe(path string) ([]byte, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	return data, err == nil, err
}

// writeAtomic writes data to path through a temp file and a rename, so a reader never
// sees half a file and a running binary is replaced, not overwritten (no "text file
// busy"). An existing file keeps its mode.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// copyAtomic copies src to dst as an executable, through a temp file and a rename.
func copyAtomic(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".gateway.tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// backupPath is the first of path.bak, path.bak.1, … that does not exist, so an older
// backup is never overwritten.
func backupPath(path string) string {
	cand := path + ".bak"
	for n := 1; ; n++ {
		if _, err := os.Lstat(cand); errors.Is(err, fs.ErrNotExist) {
			return cand
		}
		cand = path + ".bak." + strconv.Itoa(n)
	}
}

func sameFile(a, b string) bool {
	if a == b {
		return true
	}
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// dirSize is the total size of the files under dir.
func dirSize(dir string) uint64 {
	var n uint64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, ierr := d.Info(); ierr == nil {
				n += uint64(info.Size())
			}
		}
		return nil
	})
	return n
}
