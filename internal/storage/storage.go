// Package storage writes snapshot files so that readers never see a partly
// written file.
package storage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/kobadaidesu/hook-test/internal/gitrepo"
)

const (
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// EventsDir returns the directory holding the snapshots of a repository,
// given its common git directory (shared by all worktrees). It lives inside
// the git directory, so it is never part of the working tree.
func EventsDir(commonDir string) string {
	return filepath.Join(commonDir, "commitcoach", "events")
}

// EventPath returns where the snapshot of commit oid is stored.
func EventPath(commonDir, oid string) (string, error) {
	if !gitrepo.IsObjectID(oid) { // also guarantees a plain file name
		return "", fmt.Errorf("%q is not an object name", oid)
	}
	return filepath.Join(EventsDir(commonDir), oid+".json"), nil
}

// SaveEvent stores the snapshot of commit oid, replacing an earlier snapshot
// of the same commit, and returns the file path. Directories are created
// (mode 0700) only when needed.
func SaveEvent(commonDir, oid string, data []byte) (string, error) {
	path, err := EventPath(commonDir, oid)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return "", fmt.Errorf("creating the snapshot directory: %w", err)
	}
	if err := WriteFileAtomic(path, data); err != nil {
		return "", err
	}
	return path, nil
}

// CountEvents returns the number of snapshot files, or 0 if the directory
// does not exist yet.
func CountEvents(commonDir string) (int, error) {
	entries, err := os.ReadDir(EventsDir(commonDir))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return n, nil
}

// WriteFileAtomic writes data to a new temporary file (mode 0600) in the
// target directory and renames it over path. Readers see either the old file
// or the complete new one, and concurrent writers each use their own
// temporary file, so their contents never mix; the last rename wins.
func WriteFileAtomic(path string, data []byte) (err error) {
	dir, name := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
			err = fmt.Errorf("writing %s: %w", path, err)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(filePerm); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
