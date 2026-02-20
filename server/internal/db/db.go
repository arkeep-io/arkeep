package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
	gormpostgres "gorm.io/driver/postgres"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// migrationsFS embeds all SQL migration files into the binary at compile time.
// Migration files must follow the golang-migrate naming convention:
// {version}_{title}.up.sql and {version}_{title}.down.sql
//
// go:embed migrations/*.sql
var migrationsFS embed.FS

// Config holds the configuration required to open a database connection.
// Driver defaults to "sqlite" if left empty.
// DSN is the file path for SQLite (e.g. "./arkeep.db") or a standard
// PostgreSQL connection string (e.g. "postgres://user:pass@host/db?sslmode=disable").
type Config struct {
	Driver   string // "sqlite" or "postgres"
	DSN      string
	Logger   *zap.Logger
	LogLevel gormlogger.LogLevel // gormlogger.Silent | Warn | Error | Info
}

// New opens a database connection for the configured driver, applies any
// pending migrations, and returns the ready-to-use *gorm.DB instance.
//
// Callers should defer a call to close the underlying *sql.DB when done:
//
//	sqlDB, _ := db.DB()
//	defer sqlDB.Close()
func New(cfg Config) (*gorm.DB, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("db: logger is required")
	}

	gormCfg := &gorm.Config{
		Logger: newZapGORMLogger(cfg.Logger, cfg.LogLevel),
	}

	var (
		db      *gorm.DB
		sqlDB   *sql.DB
		err     error
		drvName string
	)

	switch cfg.Driver {
	case "sqlite", "":
		db, err = gorm.Open(gormsqlite.Open(cfg.DSN), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("db: failed to open sqlite: %w", err)
		}
		sqlDB, err = db.DB()
		if err != nil {
			return nil, fmt.Errorf("db: failed to get sql.DB: %w", err)
		}
		// SQLite does not support concurrent writes. Limiting to a single open
		// connection prevents "database is locked" errors under concurrent load.
		sqlDB.SetMaxOpenConns(1)
		drvName = "sqlite3"

	case "postgres":
		db, err = gorm.Open(gormpostgres.Open(cfg.DSN), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("db: failed to open postgres: %w", err)
		}
		sqlDB, err = db.DB()
		if err != nil {
			return nil, fmt.Errorf("db: failed to get sql.DB: %w", err)
		}
		// Connection pool tuned for a typical self-hosted workload.
		// Adjust via environment variables in production if needed.
		sqlDB.SetMaxOpenConns(25)
		sqlDB.SetMaxIdleConns(5)
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
		drvName = "postgres"

	default:
		return nil, fmt.Errorf("db: unsupported driver %q, use \"sqlite\" or \"postgres\"", cfg.Driver)
	}

	if err := runMigrations(sqlDB, drvName, cfg.Logger); err != nil {
		return nil, fmt.Errorf("db: migrations failed: %w", err)
	}

	return db, nil
}

// Ping verifies that the database connection is still alive.
// It is intended for use in health-check endpoints.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("db: failed to get sql.DB: %w", err)
	}
	return sqlDB.PingContext(ctx)
}

// runMigrations applies all pending up-migrations from the embedded SQL files.
// It is called automatically by New and should not be invoked directly.
// ErrNoChange is treated as success — it means the schema is already up to date.
func runMigrations(sqlDB *sql.DB, driver string, log *zap.Logger) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	var m *migrate.Migrate

	switch driver {
	case "sqlite3":
		drv, err := migratesqlite.WithInstance(sqlDB, &migratesqlite.Config{})
		if err != nil {
			return fmt.Errorf("failed to create sqlite3 migrate driver: %w", err)
		}
		m, err = migrate.NewWithInstance("iofs", src, "sqlite3", drv)
		if err != nil {
			return fmt.Errorf("failed to create migrator: %w", err)
		}

	case "postgres":
		drv, err := migratepg.WithInstance(sqlDB, &migratepg.Config{})
		if err != nil {
			return fmt.Errorf("failed to create postgres migrate driver: %w", err)
		}
		m, err = migrate.NewWithInstance("iofs", src, "postgres", drv)
		if err != nil {
			return fmt.Errorf("failed to create migrator: %w", err)
		}
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	log.Info("database migrations applied successfully")
	return nil
}