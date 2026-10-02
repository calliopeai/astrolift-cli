// Package privatefile protects credential and reviewed-request files using
// POSIX permissions or a protected Windows current-user/SYSTEM ACL.
package privatefile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Open validates the actual opened file before its content may be read.
func Open(filename string, limit int64) (*os.File, error) {
	f, err := openPrivate(filename)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || limit < 0 || info.Size() > limit {
		_ = f.Close()
		return nil, errors.New("private file must be regular and within the size limit")
	}
	return f, nil
}

// CreateExclusive never replaces an existing path. Protection is established
// before the caller writes data; the caller remains responsible for Sync/Close.
func CreateExclusive(filename string) (*os.File, error) {
	return createPrivate(filename)
}

func ReadFile(filename string, limit int64) ([]byte, error) {
	f, err := Open(filename, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("private file exceeds the size limit")
	}
	return data, nil
}

// Write installs a newly protected, flushed file. Explicit credential saves
// may replace current-user-owned legacy Windows files without reading them.
func Write(filename string, data []byte) error {
	if err := validateReplacement(filename); err != nil {
		return err
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(filename), ".astro-private-"+hex.EncodeToString(entropy[:]))
	f, err := CreateExclusive(temporary)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary) }()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := replacePrivate(temporary, filename); err != nil {
		return fmt.Errorf("installing private file: %w", err)
	}
	return SyncParent(filename)
}
