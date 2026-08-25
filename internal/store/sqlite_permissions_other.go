//go:build !linux

package store

import "os"

func sqliteFileOwnedByCurrentUser(os.FileInfo) bool {
	return true
}
