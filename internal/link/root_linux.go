//go:build linux

package link

import "os"

func isRoot() bool { return os.Geteuid() == 0 }
