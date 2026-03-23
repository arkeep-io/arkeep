// Package telemetry sends anonymous usage pings to help prioritize development.
//
// A stable random instance ID is generated on first run and persisted to
// {data_dir}/telemetry_id. The ID contains no personal information — it is a
// random UUID v4. Pings are sent once per day and contain only: the instance
// ID, Arkeep version, OS name, connected agent count, and active policy count.
//
// Telemetry is opt-out: set ARKEEP_TELEMETRY=false or --telemetry=false to
// disable it. Aggregate stats are public at https://telemetry.arkeep.io/stats
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	pingURL      = "https://telemetry.arkeep.io/ping"
	pingInterval = 24 * time.Hour
	pingTimeout  = 10 * time.Second
	idFileName   = "telemetry_id"
	idFilePerms  = 0600
)

// StatsProvider supplies runtime statistics included in telemetry pings.
// Implement this interface to avoid a direct import of internal packages.
type StatsProvider interface {
	ConnectedAgentsCount() int
	ActivePoliciesCount() int
}

// Reporter sends anonymous usage pings on a 24-hour interval.
// Create with New; start the background loop with Start.
type Reporter struct {
	version    string
	stats      StatsProvider
	logger     *zap.Logger
	instanceID string
	client     *http.Client
}

type pingPayload struct {
	InstanceID    string `json:"instance_id"`
	Version       string `json:"version"`
	AgentsCount   int    `json:"agents_count"`
	PoliciesCount int    `json:"policies_count"`
	OS            string `json:"os"`
}

// New loads or creates the stable instance ID from dataDir and returns a
// Reporter ready to start. The second return value is true when the instance
// ID was generated for the first time (i.e. this is the first run).
func New(version, dataDir string, stats StatsProvider, logger *zap.Logger) (*Reporter, bool, error) {
	r := &Reporter{
		version: version,
		stats:   stats,
		logger:  logger.Named("telemetry"),
		client:  &http.Client{Timeout: pingTimeout},
	}

	id, firstRun, err := loadOrCreateID(filepath.Join(dataDir, idFileName))
	if err != nil {
		return nil, false, fmt.Errorf("telemetry: instance id: %w", err)
	}
	r.instanceID = id

	return r, firstRun, nil
}

// Start sends an immediate ping then pings every 24 hours until ctx is done.
// Intended to be run in a goroutine.
func (r *Reporter) Start(ctx context.Context) {
	r.ping(ctx)

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.ping(ctx)
		case <-ctx.Done():
			return
		}
	}
}

func (r *Reporter) ping(ctx context.Context) {
	payload := pingPayload{
		InstanceID:    r.instanceID,
		Version:       r.version,
		AgentsCount:   r.stats.ConnectedAgentsCount(),
		PoliciesCount: r.stats.ActivePoliciesCount(),
		OS:            runtime.GOOS,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		r.logger.Debug("telemetry: failed to marshal payload", zap.Error(err))
		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, pingURL, bytes.NewReader(data))
	if err != nil {
		r.logger.Debug("telemetry: failed to create request", zap.Error(err))
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		r.logger.Debug("telemetry: ping failed", zap.Error(err))
		return
	}
	defer resp.Body.Close() //nolint:errcheck

	r.logger.Debug("telemetry: ping sent", zap.Int("status", resp.StatusCode))
}

// loadOrCreateID reads the instance ID from path. If the file does not exist
// a new UUID v4 is generated and written. Returns the ID and whether it was
// freshly created.
func loadOrCreateID(path string) (id string, created bool, err error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if id = strings.TrimSpace(string(data)); id != "" {
			return id, false, nil
		}
	} else if !os.IsNotExist(err) {
		return "", false, err
	}

	id, err = newUUID()
	if err != nil {
		return "", false, fmt.Errorf("generate uuid: %w", err)
	}

	if err = os.WriteFile(path, []byte(id), idFilePerms); err != nil {
		return "", false, fmt.Errorf("write instance id: %w", err)
	}

	return id, true, nil
}

// newUUID generates a random UUID v4 using only crypto/rand (no external deps).
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant bits (RFC 4122)
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
