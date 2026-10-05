package integration_test

import (
	"context"
	"testing"

	"github.com/arkeep-io/arkeep/server/internal/repositories"
	proto "github.com/arkeep-io/arkeep/shared/proto"
)

// TestAgentRegistration verifies the full registration lifecycle:
//
//  1. A fake agent calls Register → receives a persistent agent_id.
//  2. The agent record is visible in the DB (status "offline").
//  3. The agent opens StreamJobs → server marks it "online" in DB and in-memory.
//  4. The stream is closed from the client side → server marks it "offline" again.
func TestAgentRegistration(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	// ── Step 1: Register ──────────────────────────────────────────────────────

	agentID := agent.register(t)
	if agentID == "" {
		t.Fatal("Register returned empty agent_id")
	}

	// ── Step 2: Agent record exists in DB ─────────────────────────────────────

	id := mustParseUUID(t, agentID)
	record, err := ts.agentRepo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID after Register: %v", err)
	}
	if record.Hostname != "integration-test-host" {
		t.Errorf("hostname = %q, want integration-test-host", record.Hostname)
	}
	// Status is "offline" until StreamJobs is opened.
	if record.Status != "offline" {
		t.Errorf("status after Register = %q, want offline", record.Status)
	}

	// ── Step 3: Open stream → agent goes online ───────────────────────────────

	_, cancelStream := agent.openStream(t)

	waitForAgentStatus(t, ts.agentRepo, agentID, "online")

	if !ts.agentMgr.IsConnected(agentID) {
		t.Error("agentMgr.IsConnected = false, want true after StreamJobs")
	}

	// ── Step 4: Close stream → agent goes offline ─────────────────────────────

	cancelStream()

	waitForAgentStatus(t, ts.agentRepo, agentID, "offline")

	if ts.agentMgr.IsConnected(agentID) {
		t.Error("agentMgr.IsConnected = true, want false after stream close")
	}
}

// TestReconnect verifies that an agent can reconnect using its persisted
// agent_id: the existing DB record is updated (not duplicated) and the agent
// goes back online.
func TestReconnect(t *testing.T) {
	ts := newTestServer(t)
	agent := newFakeAgent(t, ts.addr)

	// First connection.
	agentID := agent.register(t)
	_, cancelFirst := agent.openStream(t)
	waitForAgentStatus(t, ts.agentRepo, agentID, "online")
	cancelFirst()
	waitForAgentStatus(t, ts.agentRepo, agentID, "offline")

	// Reconnect with persisted agent_id.
	resp, err := agent.client.Register(
		context.Background(),
		&proto.RegisterRequest{ //nolint:composites — proto fields use generated names
			AgentId:  agentID,
			Hostname: "integration-test-host",
			Version:  "0.0.0-test",
			Os:       "linux",
			Arch:     "amd64",
		},
	)
	if err != nil {
		t.Fatalf("reconnect Register: %v", err)
	}
	if resp.AgentId != agentID {
		t.Errorf("reconnect agent_id = %q, want %q (same as first)", resp.AgentId, agentID)
	}

	// Open stream again → agent goes online.
	_, cancelSecond := agent.openStream(t)
	defer cancelSecond()
	waitForAgentStatus(t, ts.agentRepo, agentID, "online")

	// Only one agent record in the DB.
	agents, total, err := ts.agentRepo.List(context.Background(), repositories.ListOptions{Limit: 100})
	if err != nil {
		t.Fatalf("List agents: %v", err)
	}
	if total != 1 {
		t.Errorf("agent count = %d, want 1 (no duplicates on reconnect)", total)
	}
	_ = agents
}

// TestReconnect_NameFollowsHostname verifies issue #284's migration path: an
// agent whose name is still its first-registration hostname (e.g. a Docker
// container ID) takes the new hostname on reconnect, while a name the user
// chose is left untouched.
func TestReconnect_NameFollowsHostname(t *testing.T) {
	reconnect := func(t *testing.T, ts *testServer, agentID, hostname string) {
		t.Helper()
		agent := newFakeAgent(t, ts.addr)
		_, err := agent.client.Register(context.Background(), &proto.RegisterRequest{ //nolint:composites — proto fields use generated names
			AgentId:  agentID,
			Hostname: hostname,
			Version:  "0.0.0-test",
			Os:       "linux",
			Arch:     "amd64",
		})
		if err != nil {
			t.Fatalf("reconnect Register: %v", err)
		}
	}

	t.Run("default name follows the new hostname", func(t *testing.T) {
		ts := newTestServer(t)
		agentID := newFakeAgent(t, ts.addr).register(t) // name == hostname == integration-test-host

		reconnect(t, ts, agentID, "nas-home")

		record, err := ts.agentRepo.GetByID(context.Background(), mustParseUUID(t, agentID))
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if record.Hostname != "nas-home" || record.Name != "nas-home" {
			t.Errorf("name/hostname = %q/%q, want nas-home/nas-home", record.Name, record.Hostname)
		}
	})

	t.Run("user-chosen name is kept", func(t *testing.T) {
		ts := newTestServer(t)
		agentID := newFakeAgent(t, ts.addr).register(t)
		id := mustParseUUID(t, agentID)

		record, err := ts.agentRepo.GetByID(context.Background(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		record.Name = "Backup box"
		if err := ts.agentRepo.Update(context.Background(), record); err != nil {
			t.Fatalf("rename agent: %v", err)
		}

		reconnect(t, ts, agentID, "nas-home")

		record, err = ts.agentRepo.GetByID(context.Background(), id)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if record.Name != "Backup box" {
			t.Errorf("name = %q, want the user's \"Backup box\"", record.Name)
		}
		if record.Hostname != "nas-home" {
			t.Errorf("hostname = %q, want nas-home", record.Hostname)
		}
	})
}
