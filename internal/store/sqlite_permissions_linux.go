//go:build linux

package store

import (
	"os"
	"syscall"
)

func sqliteFileOwnedByCurrentUser(info os.FileInfo) bool {
	status, ok := info.Sys().(*syscall.Stat_t)
	return ok && status.Uid == uint32(os.Geteuid())
}
