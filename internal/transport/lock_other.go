//go:build !linux

package transport

import (
	"errors"
	"os"
)

func acquireFileLock(string) (*os.File, error) {
	return nil, errors.New("NHI lifecycle changes are supported on Linux only")
}

func releaseFileLock(*os.File) {}
