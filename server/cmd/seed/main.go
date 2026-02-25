// Package main implements a one-shot seed command that creates a user directly
// in the Arkeep database. It lives inside the server module so it can access
// server/internal/* packages.
//
// Usage (from monorepo root):
//
//	go run ./server/cmd/seed \
//	  --email admin@test.com \
//	  --password secret \
//	  --name "Admin User" \
//	  --role admin
//
// Environment variables:
//
//	ARKEEP_DB_DSN      SQLite file path or Postgres DSN (default: ./arkeep.db)
//	ARKEEP_SECRET_KEY  Master encryption key — must match the value used by the server
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"
	gormlogger "gorm.io/gorm/logger"

	"github.com/arkeep-io/arkeep/server/internal/auth"
	"github.com/arkeep-io/arkeep/server/internal/db"
	"github.com/arkeep-io/arkeep/server/internal/repositories"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// ─── Flags ────────────────────────────────────────────────────────────────

	email := flag.String("email", "", "User email (required)")
	password := flag.String("password", "", "Plain-text password (required)")
	name := flag.String("name", "Admin User", "Display name")
	role := flag.String("role", "admin", "Role: admin or user")
	flag.Parse()

	if *email == "" {
		return fmt.Errorf("--email is required")
	}
	if *password == "" {
		return fmt.Errorf("--password is required")
	}
	if *role != "admin" && *role != "user" {
		return fmt.Errorf("--role must be 'admin' or 'user'")
	}

	// ─── Config ───────────────────────────────────────────────────────────────

	dsn := envOrDefault("ARKEEP_DB_DSN", "./arkeep.db")

	secretKey := os.Getenv("ARKEEP_SECRET_KEY")
	if secretKey == "" {
		return fmt.Errorf(
			"ARKEEP_SECRET_KEY is not set\n" +
				"  Set it to the same value used by the server, otherwise the\n" +
				"  encrypted password will be unreadable at login time.",
		)
	}

	// ─── Encryption ───────────────────────────────────────────────────────────

	// InitEncryption must be called before any DB operation so that
	// EncryptedString fields are encoded correctly on write.
	if err := db.InitEncryption([]byte(secretKey)); err != nil {
		return fmt.Errorf("init encryption: %w", err)
	}

	// ─── Database ─────────────────────────────────────────────────────────────

	logger, _ := zap.NewDevelopment()

	database, err := db.New(db.Config{
		Driver:   "sqlite",
		DSN:      dsn,
		Logger:   logger,
		LogLevel: gormlogger.Silent, // suppress GORM query logs in seed output
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}

	sqlDB, err := database.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	defer sqlDB.Close()

	// ─── Hash password ────────────────────────────────────────────────────────

	hashed, err := auth.HashPassword(*password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	// ─── Create user ──────────────────────────────────────────────────────────

	userRepo := repositories.NewUserRepository(database)

	user := &db.User{
		Email:       *email,
		DisplayName: *name,
		Password:    db.EncryptedString(hashed),
		Role:        *role,
		IsActive:    true,
	}

	if err := userRepo.Create(context.Background(), user); err != nil {
		if errors.Is(err, repositories.ErrConflict) {
			return fmt.Errorf("a user with email %q already exists", *email)
		}
		return fmt.Errorf("create user: %w", err)
	}

	fmt.Printf("✓ User created\n")
	fmt.Printf("  ID:    %s\n", user.ID)
	fmt.Printf("  Email: %s\n", user.Email)
	fmt.Printf("  Name:  %s\n", user.DisplayName)
	fmt.Printf("  Role:  %s\n", user.Role)

	return nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}