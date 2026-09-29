package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// InlineMax is the largest content, in bytes, stored inline in the content table.
// Anything larger goes to a zstd-compressed blob file named by its hash.
const InlineMax = 4096

// blobDirName is the blob directory's name inside the capture directory.
const blobDirName = "blobs"

// content is one item to store by hash: a raw body (hashed as captured, still
// content-encoded) or a piece of canonical parsed content.
type content struct {
	hash string
	data []byte
}

// hashOf is the content key: sha256, lowercase hex.
func hashOf(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// dedup drops repeated hashes, keeping the first of each.
func dedup(items []content) []content {
	seen := make(map[string]bool, len(items))
	out := items[:0:0]
	for _, c := range items {
		if !seen[c.hash] {
			seen[c.hash] = true
			out = append(out, c)
		}
	}
	return out
}

// writeBlobs puts every item over InlineMax on disk before any row can reference it.
// An item whose row already exists is skipped: its blob was written before the row.
func (s *Store) writeBlobs(ctx context.Context, items []content) error {
	for _, c := range items {
		if len(c.data) <= InlineMax {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		var one int
		err := s.db.QueryRowContext(ctx, `SELECT 1 FROM content WHERE hash = ?`, c.hash).Scan(&one)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("look up content %s: %w", c.hash, err)
		}
		if err := s.writeBlob(c); err != nil {
			return err
		}
	}
	return nil
}

// writeBlob writes c zstd-compressed to <dir>/blobs/<hash[0:2]>/<hash[2:]>: a temp
// file in the target directory, fsync, rename, then an fsync of the directory so the
// rename is durable before the row that references it. Two workers writing the same
// blob rename identical bytes over each other, which is harmless.
func (s *Store) writeBlob(c content) (err error) {
	target := s.blobPath(c.hash)
	parent := filepath.Dir(target)
	if err := mkdir0700(filepath.Dir(parent)); err != nil {
		return err
	}
	if err := mkdir0700(parent); err != nil {
		return err
	}
	f, err := os.CreateTemp(parent, ".tmp-*") // 0600
	if err != nil {
		return fmt.Errorf("blob %s: %w", target, err)
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(s.enc.EncodeAll(c.data, nil)); err != nil {
		return fmt.Errorf("blob %s: %w", target, err)
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("blob %s: sync: %w", target, err)
	}
	if err = f.Close(); err != nil {
		return fmt.Errorf("blob %s: %w", target, err)
	}
	if err = os.Rename(tmp, target); err != nil {
		return fmt.Errorf("blob %s: %w", target, err)
	}
	if err = syncDir(parent); err != nil {
		return fmt.Errorf("blob %s: sync dir: %w", target, err)
	}
	return nil
}

func (s *Store) blobPath(hash string) string {
	return filepath.Join(s.dir, blobDirName, hash[:2], hash[2:])
}

// mkdir0700 creates dir if it is missing and gives a new one mode 0700, which the
// umask can narrow at creation.
func mkdir0700(dir string) error {
	err := os.Mkdir(dir, 0o700)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("blob dir %s: %w", dir, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("blob dir %s: %w", dir, err)
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// insertContent writes the rows for items inside tx. A blob's file is already on
// disk; an existing row is left as it is.
func insertContent(ctx context.Context, tx *sql.Tx, items []content) error {
	for _, c := range items {
		var err error
		if len(c.data) <= InlineMax {
			data := c.data
			if data == nil {
				data = []byte{} // inline rows need a non-NULL data
			}
			_, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO content (hash, size, location, data) VALUES (?, ?, 'inline', ?)`,
				c.hash, len(data), data)
		} else {
			_, err = tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO content (hash, size, location, data) VALUES (?, ?, 'blob', NULL)`,
				c.hash, len(c.data))
		}
		if err != nil {
			return fmt.Errorf("insert content %s: %w", c.hash, err)
		}
	}
	return nil
}
