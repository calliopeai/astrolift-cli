//go:build !windows

package privatefile

import (
	"errors"
	"os"
	"path/filepath"
)

func openPrivate(filename string) (*os.File, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, errors.New("private file must be regular with mode 0600")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm() != 0600 {
		_ = f.Close()
		return nil, errors.New("private file changed while opening or is not mode 0600")
	}
	return f, nil
}

func createPrivate(filename string) (*os.File, error) {
	f, err := os.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func validateReplacement(filename string) error {
	info, err := os.Lstat(filename)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("refusing to replace a non-regular private file")
	}
	return nil
}

func replacePrivate(source, destination string) error {
	return os.Rename(source, destination)
}

// SyncParent makes successful exclusive creation/replacement durable on POSIX.
func SyncParent(filename string) error {
	dir, err := os.Open(filepath.Dir(filename))
	if err != nil {
		return err
	}
	err = dir.Sync()
	closeErr := dir.Close()
	if err != nil {
		return err
	}
	return closeErr
}
