// Package restic is the sole component responsible for interacting with the
// restic backup engine and rclone transport layer. No other package in the
// agent may import or reference restic or rclone directly — they are
// implementation details hidden behind the Wrapper interface.
//
// On first use, the binaries embedded at build time are extracted to the
// agent's state directory (see Extractor). Subsequent calls skip extraction
// if the binary is already present and has the correct size.
package restic

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// Embedded binary assets. The bin/ directory is populated by the
// `task deps:download` command before building and is excluded from git.
// Each binary is named with its target platform to allow cross-compilation:
//
//	bin/restic_linux_amd64
//	bin/restic_linux_arm64
//	bin/restic_darwin_amd64
//	bin/restic_darwin_arm64
//	bin/restic_windows_amd64.exe
//	bin/rclone_linux_amd64   (etc.)
//
// The embed pattern uses * which does not match hidden files or directories,
// so only the binary files themselves are included.
//
//go:embed all:bin
var embeddedBins embed.FS

// Extractor manages the lifecycle of the embedded binaries on disk.
// It extracts restic and rclone to the state directory on first run and
// returns their absolute paths for use by the Wrapper.
//
// The zero value is not usable — create instances with NewExtractor.
type Extractor struct {
	// stateDir is the directory where extracted binaries are written.
	// Typically ~/.arkeep or the value of --state-dir.
	stateDir string
}

// NewExtractor creates an Extractor that will write binaries to stateDir.
func NewExtractor(stateDir string) *Extractor {
	return &Extractor{stateDir: stateDir}
}

// ResticPath extracts the restic binary for the current platform (if needed)
// and returns its absolute path. Safe to call multiple times — extraction is
// skipped if the file is already present with the correct size.
func (e *Extractor) ResticPath() (string, error) {
	return e.extract("restic")
}

// RclonePath extracts the rclone binary for the current platform (if needed)
// and returns its absolute path.
func (e *Extractor) RclonePath() (string, error) {
	return e.extract("rclone")
}

// extract extracts the named binary (either "restic" or "rclone") for the
// current GOOS/GOARCH and returns its path on disk.
//
// Extraction logic:
//  1. Determine the embedded source path from runtime.GOOS and runtime.GOARCH.
//  2. Stat the destination path — if it exists and the sizes match, return
//     immediately (idempotent, fast path for normal operation).
//  3. Write the binary to a temp file in the same directory, then rename it
//     into place. The rename is atomic on POSIX systems, preventing a corrupt
//     binary if the process is interrupted mid-write.
//  4. Set executable permissions (0755) on non-Windows platforms.
func (e *Extractor) extract(name string) (string, error) {
	srcPath, err := embeddedPath(name)
	if err != nil {
		return "", err
	}

	destPath := filepath.Join(e.stateDir, binaryName(name))

	// Open the embedded source to get its size for the fast-path check.
	srcFile, err := embeddedBins.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("restic: embedded binary not found at %q: %w", srcPath, err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return "", fmt.Errorf("restic: failed to stat embedded binary %q: %w", srcPath, err)
	}

	// Reject placeholder files (size 0) committed to git as embed anchors.
	// Real binaries must be downloaded via `task deps:download` before building.
	if srcInfo.Size() == 0 {
		return "", fmt.Errorf("restic: embedded %s binary is a placeholder — run `task deps:download`", name)
	}

	// Fast path: binary already on disk with matching size — skip extraction.
	if destInfo, err := os.Stat(destPath); err == nil {
		if destInfo.Size() == srcInfo.Size() {
			return destPath, nil
		}
		// Size mismatch (e.g. after an agent upgrade) — re-extract.
	}

	// Ensure the state directory exists.
	if err := os.MkdirAll(e.stateDir, 0750); err != nil {
		return "", fmt.Errorf("restic: failed to create state dir %q: %w", e.stateDir, err)
	}

	// Write to a temp file first, then rename atomically.
	// This prevents a half-written binary from being executed if the process
	// is killed mid-extraction.
	tmpFile, err := os.CreateTemp(e.stateDir, name+".*.tmp")
	if err != nil {
		return "", fmt.Errorf("restic: failed to create temp file for %s: %w", name, err)
	}
	tmpPath := tmpFile.Name()

	// Always clean up the temp file on failure.
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, srcFile); err != nil {
		tmpFile.Close()
		return "", fmt.Errorf("restic: failed to write %s binary: %w", name, err)
	}
	if err := tmpFile.Close(); err != nil {
		return "", fmt.Errorf("restic: failed to close temp file for %s: %w", name, err)
	}

	// Set executable bit before rename so the file is never executable-but-incomplete.
	if err := setExecutable(tmpPath); err != nil {
		return "", fmt.Errorf("restic: failed to set executable permission on %s: %w", name, err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return "", fmt.Errorf("restic: failed to move %s binary to %q: %w", name, destPath, err)
	}

	success = true
	return destPath, nil
}

// embeddedPath returns the path inside the embedded FS for the given binary
// name on the current platform.
func embeddedPath(name string) (string, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Validate that we have a known combination to give a clear error message
	// rather than a confusing "file not found" from the FS.
	switch goos {
	case "linux", "darwin", "windows":
	default:
		return "", fmt.Errorf("restic: unsupported OS %q", goos)
	}
	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("restic: unsupported architecture %q", goarch)
	}

	filename := fmt.Sprintf("%s_%s_%s", name, goos, goarch)
	if goos == "windows" {
		filename += ".exe"
	}
	return filepath.Join("bin", filename), nil
}

// binaryName returns the destination filename on disk for the given binary.
// On Windows, .exe is appended so the OS can execute it directly.
func binaryName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// setExecutable sets the executable bit (0755) on the file at path.
// This is a no-op on Windows, where executability is determined by file
// extension (.exe) rather than filesystem permissions.
func setExecutable(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	return os.Chmod(path, fs.FileMode(0755))
}