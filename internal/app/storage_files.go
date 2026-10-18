package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// Sync newly created parent entries as well as the files published below them.
// Existing directories do not need an extra sync on every operation update.
func ensurePrivateDirectory(directory string) error {
	info, err := os.Stat(directory)
	if err == nil {
		if !info.IsDir() {
			return errors.New("Storage path is not a directory")
		}
		return nil
	}
	if !os.IsNotExist(err) || filepath.Dir(directory) == directory {
		return err
	}
	if err = ensurePrivateDirectory(filepath.Dir(directory)); err != nil {
		return err
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		if !os.IsExist(err) {
			return err
		}
		if info, err = os.Stat(directory); err != nil {
			return err
		} else if !info.IsDir() {
			return errors.New("Storage path is not a directory")
		}
	}
	return syncDirectory(filepath.Dir(directory))
}

// Linux is the deployment target. Windows does not support syncing directory handles.
func syncDirectory(directory string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	file, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func writePrivateFile(filename string, data []byte) error {
	file, err := os.OpenFile(filename, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func atomicJSON(filename string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(filename), ".pending-")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(temp, filename); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(filename))
}
