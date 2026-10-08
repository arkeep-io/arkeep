package repositories

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// TestDelete_LeavesNoSecretInDatabaseFile is the regression test for the
// follow-up to issue #289: wiping a deleted destination's or policy's secrets
// must also remove them from the SQLite file itself. Without secure_delete the
// old ciphertext stayed in the freed page space, readable with `strings` and
// decryptable with the server's key.
func TestDelete_LeavesNoSecretInDatabaseFile(t *testing.T) {
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("db.InitEncryption: %v", err)
	}
	path := filepath.Join(t.TempDir(), "arkeep.db")
	gdb, err := db.New(db.Config{Driver: "sqlite", DSN: path, Logger: zap.NewNop(), LogLevel: gormlogger.Silent})
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}
	ctx := context.Background()
	destRepo := NewDestinationRepository(gdb)
	policyRepo := NewPolicyRepository(gdb)
	agentRepo := NewAgentRepository(gdb)

	// Sizes that used to leave residue: a short password, and credentials the
	// size of an SFTP private key, which spill into overflow pages.
	key := strings.Repeat("private-key-material-", 150)
	dest := &db.Destination{Name: "sftp", Type: "sftp", Config: "{}", Enabled: true,
		Credentials: db.EncryptedString(`{"private_key":"` + key + `"}`), RepoPassword: "destination-repo-password"}
	other := &db.Destination{Name: "kept", Type: "local", Config: "{}", Enabled: true,
		Credentials: db.EncryptedString(`{"note":"` + strings.Repeat("live-", 200) + `"}`)}
	for _, d := range []*db.Destination{dest, other} {
		if err := destRepo.Create(ctx, d); err != nil {
			t.Fatalf("create destination: %v", err)
		}
	}
	agent := &db.Agent{Name: "agent", Status: "offline", Labels: "{}"}
	if err := agentRepo.Create(ctx, agent); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	policy := &db.Policy{Name: "p", AgentID: agent.ID, Schedule: "@daily", Sources: `["/data"]`, RepoPassword: "policy-repo-password"}
	if err := policyRepo.Create(ctx, policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}

	// The ciphertexts as stored, i.e. what an attacker would look for.
	var stored struct{ Credentials, RepoPassword string }
	if err := gdb.Raw(`SELECT credentials, repo_password FROM destinations WHERE id = ?`, dest.ID).Scan(&stored).Error; err != nil {
		t.Fatalf("read destination ciphertexts: %v", err)
	}
	var policyCipher string
	if err := gdb.Raw(`SELECT repo_password FROM policies WHERE id = ?`, policy.ID).Scan(&policyCipher).Error; err != nil {
		t.Fatalf("read policy ciphertext: %v", err)
	}
	for name, c := range map[string]string{"credentials": stored.Credentials, "repo password": stored.RepoPassword, "policy repo password": policyCipher} {
		if c == "" {
			t.Fatalf("%s ciphertext is empty before delete", name)
		}
	}

	if err := destRepo.Delete(ctx, dest.ID); err != nil {
		t.Fatalf("delete destination: %v", err)
	}
	if err := policyRepo.Delete(ctx, policy.ID); err != nil {
		t.Fatalf("delete policy: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read database file: %v", err)
	}
	for name, c := range map[string]string{"credentials": stored.Credentials, "repo password": stored.RepoPassword, "policy repo password": policyCipher} {
		// Any 40-character run of the ciphertext is enough to find it.
		if bytes.Contains(file, []byte(c[:40])) {
			t.Errorf("%s ciphertext still present in the database file after delete", name)
		}
	}
}
