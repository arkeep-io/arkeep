package housekeeping

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/db/dbtest"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

// TestRunOnce runs a sweep against a real database: only expired refresh
// tokens and notifications read more than 30 days ago are deleted.
func TestRunOnce(t *testing.T) {
	if err := db.InitEncryption(bytes.Repeat([]byte("k"), 32)); err != nil {
		t.Fatalf("InitEncryption: %v", err)
	}
	gdb := dbtest.New(t)
	ctx := context.Background()
	now := time.Now().UTC()

	user := &db.User{Email: "u@test.local", DisplayName: "U", Role: "admin", IsActive: true}
	if err := repositories.NewUserRepository(gdb).Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	tokens := repositories.NewRefreshTokenRepository(gdb)
	for hash, expires := range map[string]time.Time{"expired": now.Add(-time.Hour), "valid": now.Add(time.Hour)} {
		if err := tokens.Create(ctx, &db.RefreshToken{UserID: user.ID, TokenHash: hash, ExpiresAt: expires}); err != nil {
			t.Fatalf("create token %s: %v", hash, err)
		}
	}
	notifs := repositories.NewNotificationRepository(gdb)
	readAt := map[string]*time.Time{"old-read": ptr(now.AddDate(0, 0, -31)), "recent-read": ptr(now.AddDate(0, 0, -1)), "unread": nil}
	for title, at := range readAt {
		n := &db.Notification{UserID: user.ID, Type: "job_success", Title: title, Body: "b", Payload: "{}"}
		if err := notifs.Create(ctx, n); err != nil {
			t.Fatalf("create notification %s: %v", title, err)
		}
		// Back-date the unread one too: age alone must not delete it.
		if err := gdb.Model(n).Updates(map[string]any{"read_at": at, "created_at": now.AddDate(0, 0, -60)}).Error; err != nil {
			t.Fatalf("set read_at %s: %v", title, err)
		}
	}

	NewService(tokens, notifs, zap.NewNop()).RunOnce(ctx, now)

	if _, err := tokens.GetByHash(ctx, "expired"); !errors.Is(err, repositories.ErrNotFound) {
		t.Errorf("expired token: err = %v, want it deleted", err)
	}
	if _, err := tokens.GetByHash(ctx, "valid"); err != nil {
		t.Errorf("valid token: err = %v, want it kept", err)
	}
	var left []string
	if err := gdb.Model(&db.Notification{}).Order("title").Pluck("title", &left).Error; err != nil {
		t.Fatalf("list notifications: %v", err)
	}
	if want := []string{"recent-read", "unread"}; len(left) != 2 || left[0] != want[0] || left[1] != want[1] {
		t.Errorf("notifications left = %v, want %v", left, want)
	}
}

func ptr[T any](v T) *T { return &v }
