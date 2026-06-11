// Package db manages the database connection, migrations, and encryption
// for the Arkeep server. It supports SQLite (via modernc pure-Go driver,
// no CGO required) and PostgreSQL. Migrations are embedded in the binary
// and applied automatically on startup via golang-migrate.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	migratesqlite "github.com/golang-migrate/migrate/v4/database/sqlite"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
	gormpostgres "gorm.io/driver/postgres"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	// modernc pure-Go SQLite driver — no CGO required.
	// Registers itself as "sqlite" in database/sql.
	_ "modernc.org/sqlite"
)

//go:embed migrations
var migrationsFS embed.FS

// overlayFS presents a flat migrations/ directory to golang-migrate, overlaying
// driver-specific files from migrations/{driver}/ over the shared ones.
// Files present only in migrations/{driver}/ are also exposed.
type overlayFS struct {
	base   embed.FS
	driver string
}

func (o overlayFS) Open(name string) (fs.File, error) {
	const prefix = "migrations/"
	if strings.HasPrefix(name, prefix) && !strings.Contains(name[len(prefix):], "/") {
		driverPath := "migrations/" + o.driver + "/" + name[len(prefix):]
		if f, err := o.base.Open(driverPath); err == nil {
			return f, nil
		}
	}
	return o.base.Open(name)
}

func (o overlayFS) ReadDir(name string) ([]fs.DirEntry, error) {
	shared, err := o.base.ReadDir(name)
	if err != nil {
		return nil, err
	}
	driverEntries, _ := o.base.ReadDir(name + "/" + o.driver)

	seen := make(map[string]struct{}, len(shared))
	result := make([]fs.DirEntry, 0, len(shared)+len(driverEntries))
	for _, e := range shared {
		if !e.IsDir() {
			seen[e.Name()] = struct{}{}
			result = append(result, e)
		}
	}
	for _, e := range driverEntries {
		if !e.IsDir() {
			if _, ok := seen[e.Name()]; !ok {
				result = append(result, e)
			}
		}
	}
	return result, nil
}

// Config holds the configuration required to open a database connection.
// Driver defaults to "sqlite" if left empty.
type Config struct {
	Driver   string // "sqlite" or "postgres"
	DSN      string
	Logger   *zap.Logger
	LogLevel gormlogger.LogLevel
}

// New opens a database connection, applies pending migrations, and returns
// the ready-to-use *gorm.DB instance.
func New(cfg Config) (*gorm.DB, error) {
	if cfg.Logger == nil {
		return nil, fmt.Errorf("db: logger is required")
	}

	gormCfg := &gorm.Config{
		Logger: newZapGORMLogger(cfg.Logger, cfg.LogLevel),
	}

	var (
		database *gorm.DB
		sqlDB    *sql.DB
		err      error
		drvName  string
	)

	switch cfg.Driver {
	case "sqlite", "":
		// Open the connection manually via database/sql using the modernc driver
		// (registered as "sqlite"), then hand the existing *sql.DB to GORM so it
		// does not try to open a second connection with go-sqlite3.
		sqlDB, err = sql.Open("sqlite", cfg.DSN)
		if err != nil {
			return nil, fmt.Errorf("db: failed to open sqlite: %w", err)
		}
		// SQLite supports only one writer at a time.
		sqlDB.SetMaxOpenConns(1)

		database, err = gorm.Open(gormsqlite.Dialector{Conn: sqlDB}, gormCfg)
		if err != nil {
			return nil, fmt.Errorf("db: failed to initialize gorm with sqlite: %w", err)
		}
		// Enable foreign key enforcement. SQLite disables FK constraints by default;
		// without this, ON DELETE RESTRICT/CASCADE constraints are silently ignored.
		if err := database.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
			return nil, fmt.Errorf("db: failed to enable foreign keys: %w", err)
		}
		drvName = "sqlite"

	case "postgres":
		database, err = gorm.Open(gormpostgres.Open(cfg.DSN), gormCfg)
		if err != nil {
			return nil, fmt.Errorf("db: failed to open postgres: %w", err)
		}
		sqlDB, err = database.DB()
		if err != nil {
			return nil, fmt.Errorf("db: failed to get sql.DB: %w", err)
		}
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

	return database, nil
}

// Ping verifies that the database connection is still alive.
func Ping(ctx context.Context, database *gorm.DB) error {
	sqlDB, err := database.DB()
	if err != nil {
		return fmt.Errorf("db: failed to get sql.DB: %w", err)
	}
	return sqlDB.PingContext(ctx)
}

// runMigrations applies all pending up-migrations from the embedded SQL files.
// ErrNoChange is treated as success.
func runMigrations(sqlDB *sql.DB, driver string, log *zap.Logger) error {
	src, err := iofs.New(overlayFS{migrationsFS, driver}, "migrations")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	var m *migrate.Migrate

	switch driver {
	case "sqlite":
		// NoLock: true runs each migration without a wrapping transaction, which
		// allows PRAGMA foreign_keys = OFF to take effect when a migration needs
		// to recreate tables (the only way to change CHECK constraints in SQLite).
		drv, err := migratesqlite.WithInstance(sqlDB, &migratesqlite.Config{NoTxWrap: true})
		if err != nil {
			return fmt.Errorf("failed to create sqlite migrate driver: %w", err)
		}
		m, err = migrate.NewWithInstance("iofs", src, "sqlite", drv)
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
