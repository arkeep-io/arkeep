//go:build ignore

// download_deps.go is a standalone Go script (not part of any module) that
// downloads the restic and rclone binaries into agent/internal/restic/bin/.
// It is invoked by the Taskfile and by GoReleaser's before hook.
//
// Usage:
//
//	# Current platform only (Taskfile / local development)
//	go run ./scripts/download_deps.go
//
//	# All release platforms (GoReleaser before hook)
//	go run ./scripts/download_deps.go --all-platforms
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
	rcloneVersion = "1.73.2"
	binDir        = "internal/restic/bin"
)

// platform represents a target OS/arch combination for binary downloads.
type platform struct {
	goos   string
	goarch string
}

// releasePlatforms lists all platforms for which binaries are embedded in the
// agent for release builds. Must stay in sync with the goarch/goos matrices
// in .goreleaser.yml.
//
// Note: windows/arm64 is excluded because restic does not publish a
// windows/arm64 binary.
var releasePlatforms = []platform{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
}

func main() {
	allPlatforms := false
	for _, arg := range os.Args[1:] {
		if arg == "--all-platforms" {
			allPlatforms = true
		}
	}

	if err := os.MkdirAll(binDir, 0o755); err != nil {
		fatalf("create bin dir: %v", err)
	}

	var platforms []platform
	if allPlatforms {
		fmt.Println("Downloading binaries for all release platforms...")
		platforms = releasePlatforms
	} else {
		platforms = []platform{{runtime.GOOS, runtime.GOARCH}}
	}

	for _, p := range platforms {
		if err := downloadRestic(p.goos, p.goarch); err != nil {
			fatalf("restic %s/%s: %v", p.goos, p.goarch, err)
		}
		if err := downloadRclone(p.goos, p.goarch); err != nil {
			fatalf("rclone %s/%s: %v", p.goos, p.goarch, err)
		}
	}
}

// ─── restic ──────────────────────────────────────────────────────────────────

func downloadRestic(goos, goarch string) error {
	resticOS := normalizeOS(goos)
	ext := exeExtFor(goos)
	out := filepath.Join(binDir, fmt.Sprintf("restic_%s_%s%s", resticOS, goarch, ext))

	if fileExists(out) {
		fmt.Printf("restic already present: %s\n", out)
		return nil
	}

	fmt.Printf("Downloading restic %s for %s/%s...\n", resticVersion, resticOS, goarch)

	if goos == "windows" {
		return downloadResticZip(resticOS, goarch, out)
	}
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

func downloadRclone(goos, goarch string) error {
	rcloneOS := rclonePlatform(goos)
	ext := exeExtFor(goos)
	out := filepath.Join(binDir, fmt.Sprintf("rclone_%s_%s%s", goos, goarch, ext))

	if fileExists(out) {
		fmt.Printf("rclone already present: %s\n", out)
		return nil
	}

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

func fetch(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: unexpected status %s", url, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return data, nil
}

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
			defer func() { _ = rc.Close() }()
			return io.ReadAll(rc)
		}
	}

	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return nil, fmt.Errorf("file %q not found in zip; available: %s", target, strings.Join(names, ", "))
}

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
			defer func() { _ = rc.Close() }()
			return io.ReadAll(rc)
		}
	}

	var names []string
	for _, f := range r.File {
		names = append(names, f.Name)
	}
	return nil, fmt.Errorf("no file ending with %q found in zip; available: %s", suffix, strings.Join(names, ", "))
}

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

func exeExtFor(goos string) string {
	if goos == "windows" {
		return ".exe"
	}
	return ""
}

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