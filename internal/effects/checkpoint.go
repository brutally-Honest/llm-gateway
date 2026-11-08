package effects

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"go.uber.org/zap"

	"github.com/brutally-honest/llm-gateway/internal/core"
	"github.com/brutally-honest/llm-gateway/internal/store"
)

// headBytes is how much of a file is read to tell text from binary: a NUL byte in it
// makes the file binary (AC5).
const headBytes = 8 << 10

// tombstone is the size of a snapshot row for a tracked file that is deleted.
const tombstone = -1

// fileState is one file in a session's snapshot.
type fileState struct {
	size, mtimeNS  int64
	hash, copyHash string
}

// checkpoint compares s's watched folders with its snapshot and saves the effects and the
// changed rows in one transaction (AC2, AC3). A root with no saved marker is a baseline:
// its state is recorded and no effect is stored for it. The in-memory snapshot moves only
// once the save succeeded, so a failed or interrupted checkpoint is redone from the last
// saved one (E17).
func (r *Runner) checkpoint(ctx context.Context, s *session) error {
	now := r.now()
	cp := store.Checkpoint{}
	// next holds the rows of each root as they will be after this checkpoint.
	next := map[string]map[string]fileState{}
	for _, root := range s.roots.list() {
		rows, baseline := s.snap[root], false
		if rows == nil {
			baseline = true
			rows = map[string]fileState{}
			cp.Upserts = append(cp.Upserts, store.SnapshotRow{Root: root})
		}
		out, err := r.checkRoot(ctx, s, root, rows, baseline, now, &cp)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.gitFailures.Add(1)
			r.log.Warn("effects: folder skipped for this checkpoint", zap.String("root", root), zap.Error(err))
			next[root] = s.snap[root]
			if baseline {
				cp.Upserts = cp.Upserts[:len(cp.Upserts)-1]
				delete(next, root)
			}
			continue
		}
		next[root] = out
	}
	// Roots no longer watched (folded into a wider one) lose their rows.
	for root, rows := range s.snap {
		if _, ok := next[root]; ok {
			continue
		}
		cp.Deletes = append(cp.Deletes, store.SnapshotRow{Root: root})
		for p := range rows {
			cp.Deletes = append(cp.Deletes, store.SnapshotRow{Root: root, Path: p})
		}
	}
	for i := range cp.Effects {
		cp.Effects[i].Principal, cp.Effects[i].Client, cp.Effects[i].SessionKey = s.ident.Principal, s.ident.Client, s.ident.Key
	}
	if len(cp.Effects) == 0 && len(cp.Upserts) == 0 && len(cp.Deletes) == 0 {
		return nil
	}
	if err := r.store.SaveCheckpoint(ctx, s.ident, cp); err != nil {
		return err
	}
	s.snap = map[string]map[string]fileState{}
	for root, rows := range next {
		if rows != nil {
			s.snap[root] = rows
		}
	}
	r.effectsStored.Add(int64(len(cp.Effects)))
	if len(cp.Effects) > 0 && r.onSaved != nil {
		r.onSaved(s.ident.Principal, s.ident.Client, s.ident.Key)
	}
	return nil
}

// checkRoot checks one watched folder against its saved rows and returns its rows after
// this checkpoint; effects, copies and changed rows go into cp.
func (r *Runner) checkRoot(ctx context.Context, s *session, root string, saved map[string]fileState, baseline bool, now time.Time, cp *store.Checkpoint) (map[string]fileState, error) {
	if _, err := os.Stat(root); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return saved, nil // a folder that is gone is checked again when it is back
		}
		return nil, err
	}
	top, isGit := s.top(ctx, r.git, root)
	mode := "scan"
	tracked := map[string]bool{}
	var candidates []string
	if isGit {
		mode = "git"
		entries, err := r.git.candidates(ctx, top)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			abs := filepath.Join(top, filepath.FromSlash(e.path))
			if !within(abs, root) {
				continue
			}
			rel, err := filepath.Rel(root, abs)
			if err != nil {
				continue
			}
			candidates = append(candidates, rel)
			tracked[rel] = e.tracked
		}
		// Paths seen before are checked again even when git no longer lists them (E4).
		for p := range saved {
			candidates = append(candidates, p)
		}
	} else {
		var err error
		if candidates, err = scan(ctx, root, r.ignore); err != nil {
			return nil, err
		}
		for p := range saved {
			candidates = append(candidates, p)
		}
	}

	out := make(map[string]fileState, len(saved))
	for p, st := range saved {
		out[p] = st
	}
	done := map[string]bool{}
	for _, rel := range candidates {
		if done[rel] || ignored(rel, r.ignore) {
			continue
		}
		done[rel] = true
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		abs := filepath.Join(root, rel)
		prev, seen := saved[rel]
		fi, err := os.Lstat(abs)
		if err != nil || !fi.Mode().IsRegular() {
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				continue // unreadable now; checked again next time
			}
			// A tracked file git still lists as deleted keeps a tombstone row, so it is
			// reported once, not at every checkpoint.
			gitGone := isGit && tracked[rel]
			if (seen && prev.size < 0) || (!seen && !gitGone) {
				continue
			}
			if gitGone {
				out[rel] = fileState{size: tombstone}
				cp.Upserts = append(cp.Upserts, store.SnapshotRow{Root: root, Path: rel, Size: tombstone})
			} else {
				delete(out, rel)
				cp.Deletes = append(cp.Deletes, store.SnapshotRow{Root: root, Path: rel})
			}
			if !baseline {
				e := core.Effect{Kind: core.EffectFileChange, At: now, Root: root, Path: abs, Change: core.FileDeleted,
					Mode: mode, BeforeHash: prev.copyHash}
				if e.BeforeHash == "" && gitGone {
					e.BeforeHash = r.headVersion(ctx, top, root, rel, cp)
				}
				cp.Effects = append(cp.Effects, e)
			}
			continue
		}
		if seen && prev.size < 0 {
			seen, prev = false, fileState{} // back after a delete: a create
		}
		size, mtime := fi.Size(), fi.ModTime().UnixNano()
		if seen && prev.size == size && prev.mtimeNS == mtime {
			continue
		}
		hash, data, err := r.readFile(abs, size)
		if err != nil {
			continue // unreadable now; checked again next time
		}
		st := fileState{size: size, mtimeNS: mtime, hash: hash}
		changed := !seen || prev.hash != hash
		if changed && !baseline && data != nil {
			st.copyHash = store.ContentHash(data)
			cp.Copies = append(cp.Copies, data)
		} else if !changed {
			st.copyHash = prev.copyHash
		}
		out[rel] = st
		cp.Upserts = append(cp.Upserts, store.SnapshotRow{Root: root, Path: rel, Size: size, MtimeNS: mtime, Hash: hash, CopyHash: st.copyHash})
		if !changed || baseline {
			continue
		}
		e := core.Effect{Kind: core.EffectFileChange, At: now, Root: root, Path: abs, Size: size, Mode: mode, AfterHash: st.copyHash}
		switch {
		case seen:
			e.Change = core.FileModified
			e.BeforeHash = prev.copyHash
		case isGit && tracked[rel]:
			e.Change = core.FileModified
		default:
			e.Change = core.FileCreated
		}
		if e.BeforeHash == "" && isGit && (seen || tracked[rel]) {
			e.BeforeHash = r.headVersion(ctx, top, root, rel, cp)
		}
		cp.Effects = append(cp.Effects, e)
	}
	return out, nil
}

// readFile hashes the file at path. A text file (no NUL in its head) no larger than the
// copy limit is read whole and returned as its copy; anything else is hashed as a stream
// and has no copy (E3).
func (r *Runner) readFile(path string, size int64) (string, []byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, headBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", nil, err
	}
	head = head[:n]
	text := !bytes.Contains(head, []byte{0})
	if text && size <= r.maxCopy {
		rest, err := io.ReadAll(io.LimitReader(f, r.maxCopy-int64(n)+1))
		if err != nil {
			return "", nil, err
		}
		data := append(head, rest...)
		if int64(len(data)) <= r.maxCopy {
			return store.ContentHash(data), data, nil
		}
		h := sha256.New()
		_, _ = h.Write(data)
		if _, err := io.Copy(h, f); err != nil {
			return "", nil, err
		}
		return hex.EncodeToString(h.Sum(nil)), nil, nil
	}
	h := sha256.New()
	_, _ = h.Write(head)
	if _, err := io.Copy(h, f); err != nil {
		return "", nil, err
	}
	return hex.EncodeToString(h.Sum(nil)), nil, nil
}

// headVersion stores the last committed version of a text file under the copy limit as
// the effect's before version, and returns its hash; "" when there is none (AC5).
func (r *Runner) headVersion(ctx context.Context, top, root, rel string, cp *store.Checkpoint) string {
	fromTop, err := filepath.Rel(top, filepath.Join(root, rel))
	if err != nil {
		return ""
	}
	data, ok := r.git.show(ctx, top, fromTop)
	if !ok || int64(len(data)) > r.maxCopy || bytes.Contains(data[:min(len(data), headBytes)], []byte{0}) {
		return ""
	}
	cp.Copies = append(cp.Copies, data)
	return store.ContentHash(data)
}
