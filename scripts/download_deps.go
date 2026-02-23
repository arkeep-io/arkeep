//go:build ignore

// download_deps.go is a standalone Go script (not part of any module) that
// downloads the restic and rclone binaries for the current platform into
// agent/internal/restic/bin/. It is invoked by the Taskfile:
//
//	go run ./scripts/download_deps.go
//
// Using a Go script instead of shell/cmd.exe commands guarantees identical
// behaviour on Linux, macOS, and Windows without any external tools beyond
// the Go toolchain itself.
//
// Restic release format per platform:
//   - Linux/macOS: restic_<ver>_<os>_<arch>.bz2  (single binary, bzip2-compressed)
//   - Windows:     restic_<ver>_windows_<arch>.zip (zip containing restic.exe)
//
// Rclone release format (all platforms):
//   - rclone-v<ver>-<os>-<arch>.zip (zip containing <archive-name>/rclone[.exe])
package main

import (
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	resticVersion = "0.18.1"
	rcloneVersion = "1.73.1"
	binDir        = "agent/internal/restic/bin"
)

func main() {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		fatalf("create bin dir: %v", err)
	}

	if err := downloadRestic(); err != nil {
		fatalf("restic: %v", err)
	}

	if err := downloadRclone(); err != nil {
		fatalf("rclone: %v", err)
	}
}

// ─── restic ──────────────────────────────────────────────────────────────────

func downloadRestic() error {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	resticOS := normalizeOS(goos)
	ext := exeExt()
	out := filepath.Join(binDir, fmt.Sprintf("restic_%s_%s%s", resticOS, goarch, ext))

	if fileExists(out) {
		fmt.Printf("restic already present: %s\n", out)
		return nil
	}

	fmt.Printf("Downloading restic %s for %s/%s...\n", resticVersion, resticOS, goarch)

	if goos == "windows" {
		// Windows ships a zip archive containing restic.exe directly.
		return downloadResticZip(resticOS, goarch, out)
	}

	// Linux and macOS ship a bzip2-compressed single binary (not a tar).
	return downloadResticBz2(resticOS, goarch, out)
}

func downloadResticBz2(resticOS, goarch, out string) error {
	archive := fmt.Sprintf("restic_%s_%s_%s.bz2", resticVersion, resticOS, goarch)
	url := fmt.Sprintf("https://github.com/restic/restic/releases/download/v%s/%s", resticVersion, archive)

	data, err := fetch(url)
	if err != nil {
		return err
	}

	decompressed, err := io.ReadAll(bzip2.NewReader(bytes.NewReader(data)))
	if err != nil {
		return fmt.Errorf("decompress bzip2: %w", err)
	}

	return writeExecutable(out, decompressed)
}

func downloadResticZip(resticOS, goarch, out string) error {
	// Windows zip name: restic_0.18.1_windows_amd64.zip
	// The zip contains a single file: restic_0.18.1_windows_amd64\restic.exe
	archiveName := fmt.Sprintf("restic_%s_%s_%s", resticVersion, resticOS, goarch)
	url := fmt.Sprintf("https://github.com/restic/restic/releases/download/v%s/%s.zip", resticVersion, archiveName)

	data, err := fetch(url)
	if err != nil {
		return err
	}

	// The exe inside the zip is named restic_<ver>_windows_<arch>.exe —
	// search by .exe suffix since there is exactly one exe in the archive
	// and the full name includes the version string (changes across releases).
	extracted, err := extractFromZipBySuffix(data, ".exe")
	if err != nil {
		return fmt.Errorf("extract from zip: %w", err)
	}

	return writeExecutable(out, extracted)
}

// ─── rclone ──────────────────────────────────────────────────────────────────

func downloadRclone() error {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	rcloneOS := rclonePlatform(goos)
	ext := exeExt()
	out := filepath.Join(binDir, fmt.Sprintf("rclone_%s_%s%s", rcloneOS, goarch, ext))

	if fileExists(out) {
		fmt.Printf("rclone already present: %s\n", out)
		return nil
	}

	// rclone-v1.73.1-windows-amd64.zip
	archiveName := fmt.Sprintf("rclone-v%s-%s-%s", rcloneVersion, rcloneOS, goarch)
	url := fmt.Sprintf("https://downloads.rclone.org/v%s/%s.zip", rcloneVersion, archiveName)

	fmt.Printf("Downloading rclone %s for %s/%s...\n", rcloneVersion, rcloneOS, goarch)

	data, err := fetch(url)
	if err != nil {
		return err
	}

	// rclone zip contains a subdirectory: <archiveName>/rclone[.exe]
	target := archiveName + "/rclone" + ext
	extracted, err := extractFromZip(data, target)
	if err != nil {
		return fmt.Errorf("extract from zip: %w", err)
	}

	return writeExecutable(out, extracted)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// fetch downloads url and returns the raw bytes.
func fetch(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return data, nil
}

// extractFromZip finds a file by exact path inside a zip archive and returns
// its contents. Path separators are normalised before comparison.
func extractFromZip(data []byte, target string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	target = filepath.ToSlash(target)
	for _, f := range r.File {
		if filepath.ToSlash(f.Name) == target {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return nil, fmt.Errorf("file %q not found in zip; available: %s", target, strings.Join(names, ", "))
}

// extractFromZipBySuffix finds the first file whose name ends with suffix
// (case-insensitive) and returns its contents. Useful when the enclosing
// directory name is not known in advance (e.g. restic Windows zip layout
// changed between releases).
func extractFromZipBySuffix(data []byte, suffix string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}

	suffix = strings.ToLower(suffix)
	for _, f := range r.File {
		if strings.HasSuffix(strings.ToLower(f.Name), suffix) {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}

	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return nil, fmt.Errorf("no file ending with %q found in zip; available: %s", suffix, strings.Join(names, ", "))
}

// writeExecutable writes data to path and sets the executable bit on Unix.
func writeExecutable(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("Written: %s\n", path)
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func exeExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// normalizeOS maps GOOS to the platform name used in restic release filenames.
func normalizeOS(goos string) string {
	switch goos {
	case "darwin":
		return "darwin"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

// rclonePlatform maps GOOS to the platform name used in rclone release filenames.
// rclone uses "osx" for macOS instead of "darwin".
func rclonePlatform(goos string) string {
	switch goos {
	case "darwin":
		return "osx"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}