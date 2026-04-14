package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm"

	"github.com/arkeep-io/arkeep/server/internal/agentmanager"
	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
	"github.com/arkeep-io/arkeep/server/internal/scheduler"
	"github.com/arkeep-io/arkeep/server/internal/websocket"
)

// TestMain initialises the AES encryption key required by EncryptedString
// fields (passwords, credentials, repo passwords) before any test runs.
// The key is fixed and arbitrary — security is irrelevant in an in-memory
// test database.
func TestMain(m *testing.M) {
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		panic("db.InitEncryption: " + err.Error())
	}
	os.Exit(m.Run())
}

// ─── Database ─────────────────────────────────────────────────────────────────

func newTestGormDB(t *testing.T) (*gorm.DB, *sql.DB) {
	t.Helper()
	gdb, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      ":memory:",
		Logger:   zap.NewNop(),
		LogLevel: gormlogger.Silent,
	})
	if err != nil {
		t.Fatalf("newTestGormDB: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("newTestGormDB: sql.DB: %v", err)
	}
	return gdb, sqlDB
}

// ─── Repositories ─────────────────────────────────────────────────────────────

type testDeps struct {
	gdb      *gorm.DB
	sqlDB    *sql.DB
	users    repositories.UserRepository
	tokens   repositories.RefreshTokenRepository
	agents   repositories.AgentRepository
	dests    repositories.DestinationRepository
	policies repositories.PolicyRepository
	jobs     repositories.JobRepository
	snaps    repositories.SnapshotRepository
	notifs   repositories.NotificationRepository
	oidc     repositories.OIDCProviderRepository
	settings repositories.SettingsRepository
	audit    repositories.AuditRepository
	dash     repositories.DashboardRepository
}

func newTestDeps(t *testing.T) *testDeps {
	t.Helper()
	gdb, sqlDB := newTestGormDB(t)
	return &testDeps{
		gdb:      gdb,
		sqlDB:    sqlDB,
		users:    repositories.NewUserRepository(gdb),
		tokens:   repositories.NewRefreshTokenRepository(gdb),
		agents:   repositories.NewAgentRepository(gdb),
		dests:    repositories.NewDestinationRepository(gdb),
		policies: repositories.NewPolicyRepository(gdb),
		jobs:     repositories.NewJobRepository(gdb),
		snaps:    repositories.NewSnapshotRepository(gdb),
		notifs:   repositories.NewNotificationRepository(gdb),
		oidc:     repositories.NewOIDCProviderRepository(gdb),
		settings: repositories.NewSettingsRepository(gdb),
		audit:    repositories.NewAuditRepository(gdb),
		dash:     repositories.NewDashboardRepository(gdb),
	}
}

// ─── Auth service ─────────────────────────────────────────────────────────────

func newTestAuthService(t *testing.T, deps *testDeps) *auth.AuthService {
	t.Helper()
	logger := zap.NewNop()

	jwtMgr, err := auth.NewJWTManagerGenerated("arkeep-test")
	if err != nil {
		t.Fatalf("newTestAuthService: jwt: %v", err)
	}

	denylist := auth.NewDenylist()
	t.Cleanup(denylist.Stop)

	local := auth.NewLocalAuthProvider(deps.users, deps.tokens, jwtMgr, logger)
	oidcProv := auth.NewOIDCAuthProvider(deps.oidc, deps.users, deps.tokens, jwtMgr, logger)
	return auth.NewAuthService(local, oidcProv, deps.tokens, jwtMgr, denylist)
}

// ─── Scheduler ────────────────────────────────────────────────────────────────

// newTestScheduler creates a real but unstarted Scheduler. Handler calls to
// AddPolicy / RemovePolicy / UpdatePolicy register gocron jobs without firing
// them (no Start() is called), so tests remain deterministic and fast.
func newTestScheduler(t *testing.T, deps *testDeps, mgr *agentmanager.Manager) *scheduler.Scheduler {
	t.Helper()
	sched, err := scheduler.New(deps.policies, deps.jobs, deps.dests, mgr, zap.NewNop())
	if err != nil {
		t.Fatalf("newTestScheduler: %v", err)
	}
	return sched
}

// ─── Test environment ─────────────────────────────────────────────────────────

// testEnv groups all shared test dependencies and the running HTTP server.
type testEnv struct {
	*httptest.Server
	deps    *testDeps
	authSvc *auth.AuthService
	sched   *scheduler.Scheduler
	mgr     *agentmanager.Manager
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	deps := newTestDeps(t)
	authSvc := newTestAuthService(t, deps)
	mgr := agentmanager.New(zap.NewNop())
	sched := newTestScheduler(t, deps, mgr)
	hub := websocket.NewHub()

	cfg := RouterConfig{
		AuthService:   authSvc,
		Scheduler:     sched,
		AgentManager:  mgr,
		Logger:        zap.NewNop(),
		Hub:           hub,
		Users:         deps.users,
		Agents:        deps.agents,
		Destinations:  deps.dests,
		Policies:      deps.policies,
		Jobs:          deps.jobs,
		Snapshots:     deps.snaps,
		Notifications: deps.notifs,
		OIDCProviders: deps.oidc,
		Settings:      deps.settings,
		Dashboard:     deps.dash,
		Audit:         deps.audit,
		Secure:        false,
		AutoCerts:     nil,
		ServerVersion: "0.0.0-test",
		DB:            deps.sqlDB,
	}

	srv := httptest.NewServer(NewRouter(cfg))
	t.Cleanup(srv.Close)

	return &testEnv{
		Server:  srv,
		deps:    deps,
		authSvc: authSvc,
		sched:   sched,
		mgr:     mgr,
	}
}

// ─── Token helpers ────────────────────────────────────────────────────────────

// issueToken mints a real JWT through the test auth service's JWTManager.
// The token passes Authenticate middleware without any mocking.
func (e *testEnv) issueToken(t *testing.T, userID, email, role string) string {
	t.Helper()
	tok, err := e.authSvc.JWTManager().GenerateAccessToken(userID, email, role)
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}
	return tok
}

func (e *testEnv) adminToken(t *testing.T) string {
	t.Helper()
	return e.issueToken(t, uuid.NewString(), "admin@test.local", "admin")
}

func (e *testEnv) userToken(t *testing.T) string {
	t.Helper()
	return e.issueToken(t, uuid.NewString(), "user@test.local", "user")
}

// tokenForUser mints a token whose UserID matches the given DB user's UUID.
// Required when the handler reads claimsFromCtx(r).UserID (e.g. users/me).
func (e *testEnv) tokenForUser(t *testing.T, userID uuid.UUID, role string) string {
	t.Helper()
	return e.issueToken(t, userID.String(), "test@test.local", role)
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func (e *testEnv) get(t *testing.T, path, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (e *testEnv) post(t *testing.T, path, token string, body any) *http.Response {
	t.Helper()
	return e.doJSON(t, http.MethodPost, path, token, body)
}

func (e *testEnv) patch(t *testing.T, path, token string, body any) *http.Response {
	t.Helper()
	return e.doJSON(t, http.MethodPatch, path, token, body)
}

func (e *testEnv) del(t *testing.T, path, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, e.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", path, err)
	}
	return resp
}

func (e *testEnv) doJSON(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("doJSON marshal: %v", err)
		}
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, e.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// postWithCookie sends a POST with an additional cookie. Used for refresh/
// logout tests that rely on the httpOnly refresh-token cookie.
func (e *testEnv) postWithCookie(t *testing.T, path, token string, cookie *http.Cookie, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(http.MethodPost, e.URL+path, r)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// ─── Response assertions ──────────────────────────────────────────────────────

// assertStatus fails if resp.StatusCode != want and prints the response body.
func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Errorf("status = %d, want %d; body: %s", resp.StatusCode, want, strings.TrimSpace(string(body)))
	}
}

// decodeData unmarshals {"data": ...} into dst.
func decodeData(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer resp.Body.Close()
	var env map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatalf("decodeData: decode envelope: %v", err)
	}
	raw, ok := env["data"]
	if !ok {
		t.Fatalf("decodeData: response missing 'data' key")
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("decodeData: unmarshal: %v", err)
	}
}

// ─── Fixture helpers ──────────────────────────────────────────────────────────

// createDBUser inserts a user into the database with a hashed password and
// returns the new user's ID. Note: auth.HashPassword uses Argon2 (~100 ms).
func createDBUser(t *testing.T, deps *testDeps, email, role string) uuid.UUID {
	t.Helper()
	hash, err := auth.HashPassword("test-password-123")
	if err != nil {
		t.Fatalf("createDBUser: hash: %v", err)
	}
	u := &db.User{
		DisplayName: "Test User",
		Email:       email,
		Password:    db.EncryptedString(hash),
		Role:        role,
		IsActive:    true,
	}
	if err := deps.users.Create(context.Background(), u); err != nil {
		t.Fatalf("createDBUser: create: %v", err)
	}
	return u.ID
}

// createDBAgent inserts an agent record and returns it.
func createDBAgent(t *testing.T, deps *testDeps, name string) *db.Agent {
	t.Helper()
	a := &db.Agent{Name: name, Status: "offline", Labels: "{}"}
	if err := deps.agents.Create(context.Background(), a); err != nil {
		t.Fatalf("createDBAgent: %v", err)
	}
	return a
}
