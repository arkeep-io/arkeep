package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type versionHandler struct {
	serverVersion string
	mu            sync.RWMutex
	cachedLatest  string
	cachedAt      time.Time
}

type versionResponse struct {
	ServerVersion   string `json:"server_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
}

func newVersionHandler(serverVersion string) *versionHandler {
	return &versionHandler{serverVersion: serverVersion}
}

// Get handles GET /api/v1/version.
// Returns the running server version, the latest published release, and whether
// an update is available. The latest version is fetched from the GitHub releases
// API and cached for one hour to avoid rate-limiting.
func (h *versionHandler) Get(w http.ResponseWriter, r *http.Request) {
	latest := h.getLatest()
	updateAvailable := compareServerVersions(h.serverVersion, latest) < 0
	Ok(w, versionResponse{
		ServerVersion:   h.serverVersion,
		LatestVersion:   latest,
		UpdateAvailable: updateAvailable,
	})
}

// getLatest returns the cached latest version string, refreshing from GitHub
// if the cache is empty or older than one hour.
func (h *versionHandler) getLatest() string {
	h.mu.RLock()
	if h.cachedLatest != "" && time.Since(h.cachedAt) < time.Hour {
		v := h.cachedLatest
		h.mu.RUnlock()
		return v
	}
	h.mu.RUnlock()

	latest := h.fetchFromGitHub()

	h.mu.Lock()
	h.cachedLatest = latest
	h.cachedAt = time.Now()
	h.mu.Unlock()

	return latest
}

// fetchFromGitHub calls the GitHub releases API to retrieve the latest tag.
// Falls back to the server's own version on any error so update_available is
// always false rather than returning a misleading result.
func (h *versionHandler) fetchFromGitHub() string {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest(http.MethodGet,
		"https://api.github.com/repos/arkeep-io/arkeep/releases/latest", nil)
	if err != nil {
		return h.serverVersion
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := client.Do(req)
	if err != nil {
		return h.serverVersion
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return h.serverVersion
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return h.serverVersion
	}
	tag := strings.TrimPrefix(payload.TagName, "v")
	if tag == "" {
		return h.serverVersion
	}
	return tag
}

// compareServerVersions compares two semver strings (with or without a leading
// 'v'). Returns -1 if a < b, 0 if a == b, +1 if a > b. Pre-release suffixes
// (e.g. -rc1) are ignored. Invalid segments are treated as 0.
func compareServerVersions(a, b string) int {
	pa := parseSemverParts(a)
	pb := parseSemverParts(b)
	for i := range pa {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseSemverParts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	var out [3]int
	for i, p := range parts {
		if i >= 3 {
			break
		}
		// Strip pre-release suffix (e.g. "1-rc1" → "1").
		p = strings.SplitN(p, "-", 2)[0]
		out[i], _ = strconv.Atoi(p)
	}
	return out
}
