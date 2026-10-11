package main

import (
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Discovery is done once during Linux capability initialization, never for
// each incoming message. It only reads existing files; installation is explicit.
func defaultLinuxNotificationIcon() string {
	home, _ := os.UserHomeDir()
	executable, _ := os.Executable()
	workingDirectory, _ := os.Getwd()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(dataHome) && filepath.IsAbs(home) {
		dataHome = filepath.Join(home, ".local", "share")
	}
	dataDirectories := os.Getenv("XDG_DATA_DIRS")
	if dataDirectories == "" {
		dataDirectories = "/usr/local/share:/usr/share"
	}
	// Bound optional system search roots and file probes, including a malformed
	// environment containing many entries. XDG requires absolute directories.
	roots := []string{dataHome}
	for index, directory := range strings.SplitN(dataDirectories, ":", 9) {
		if index < 8 && filepath.IsAbs(directory) {
			roots = append(roots, directory)
		}
	}
	return discoverLinuxNotificationIcon(home, executable, workingDirectory, roots)
}

func discoverLinuxNotificationIcon(home, executable, workingDirectory string, dataDirectories []string) string {
	var candidates []string
	// Prefer the dedicated small artwork across all locations before falling
	// back to branding icons, retaining location priority within each group.
	for _, names := range [][]string{{"walite-notification.png"}, {"walite.png", "walite-icon.png"}} {
		for _, directory := range dataDirectories {
			if !filepath.IsAbs(directory) {
				continue
			}
			candidates = appendNotificationIconCandidates(candidates, filepath.Join(directory, "icons"), names...)
			for _, name := range names {
				candidates = append(candidates, filepath.Join(directory, "pixmaps", name))
			}
		}
		if filepath.IsAbs(home) {
			candidates = appendNotificationIconCandidates(candidates, filepath.Join(home, ".icons"), names...)
		}
		// walite.png remains a theme alias; adjacent/repository fallback uses
		// the original walite-icon.png filename, as before.
		localName := names[len(names)-1]
		if filepath.IsAbs(executable) {
			candidates = append(candidates, filepath.Join(filepath.Dir(executable), localName), filepath.Join(filepath.Dir(executable), "assets", localName))
		}
		if filepath.IsAbs(workingDirectory) {
			candidates = append(candidates, filepath.Join(workingDirectory, "assets", localName))
		}
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		file, err := os.Open(candidate)
		if err != nil {
			continue
		}
		config, err := png.DecodeConfig(io.LimitReader(file, 64*1024))
		_ = file.Close()
		if err == nil && config.Width > 0 && config.Height > 0 {
			return candidate
		}
	}
	return ""
}

func appendNotificationIconCandidates(candidates []string, directory string, names ...string) []string {
	for _, name := range names {
		candidates = append(candidates, filepath.Join(directory, name))
		for _, size := range []string{"256x256", "512x512", "128x128", "64x64", "48x48"} {
			candidates = append(candidates, filepath.Join(directory, "hicolor", size, "apps", name))
		}
	}
	return candidates
}
