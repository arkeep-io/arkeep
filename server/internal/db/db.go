// Package db manages the database connection, migrations, and encryption
// for the Arkeep server. It supports SQLite (via modernc pure-Go driver,
// no CGO required) and PostgreSQL. Migrations are embedded in the binary
// and applied automatically on startup via golang-migrate.
package db

import (
	"database/sql"
	"embed"
	"errors"
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
		sqlDB, err = sql.Open("sqlite", withSQLitePragmas(cfg.DSN))
		if err != nil {
			return nil, fmt.Errorf("db: failed to open sqlite: %w", err)
		}
		// SQLite supports only one writer at a time.
		sqlDB.SetMaxOpenConns(1)

		database, err = gorm.Open(gormsqlite.Dialector{Conn: sqlDB}, gormCfg)
		if err != nil {
			return nil, fmt.Errorf("db: failed to initialize gorm with sqlite: %w", err)
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

	// Every time goes to the database in UTC, whatever the server's zone.
	database.ConnPool = &utcConnPool{database.ConnPool}
	database.Statement.ConnPool = database.ConnPool

	return database, nil
}

// withSQLitePragmas adds to dsn the pragmas the modernc driver runs on every
// connection it opens. Running them once after sql.Open is not enough:
// database/sql replaces a connection whose query was interrupted by its
// context, and the new connection would start with the SQLite defaults.
//
//   - foreign_keys: SQLite does not enforce foreign keys by default; without
//     it ON DELETE CASCADE is ignored and deletes leave orphan rows.
//   - secure_delete: overwrite freed content with zeros, so a value removed by
//     an UPDATE or DELETE, such as the credentials wiped when a destination is
//     deleted (issue #289), does not stay readable in the database file.
func withSQLitePragmas(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "_pragma=foreign_keys(1)&_pragma=secure_delete(1)"
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

	if driver == "sqlite" {
		if err := repairSQLiteMigration43(m, sqlDB, log); err != nil {
			return fmt.Errorf("failed to repair migration 43: %w", err)
		}
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	log.Info("database migrations applied successfully")
	return nil
}

// repairSQLiteMigration43 undoes what the SQLite migration 000043 of v0.12.0
// left behind when it stopped at the copy of notification_delivery_queue,
// which fails on a queue holding deliveries of deleted notifications: version
// 43 marked dirty, notification_providers and notification_delivery_queue_new
// created, the original queue untouched. It drops the two new tables and sets
// the version back to 42, so m.Up applies the corrected 000043. Both tables
// are empty: the server never started on that schema.
//
// It does nothing on any other state, which m.Up then reports as dirty.
func repairSQLiteMigration43(m *migrate.Migrate, sqlDB *sql.DB, log *zap.Logger) error {
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return nil
	}
	if err != nil {
		return err
	}
	if version != 43 || !dirty {
		return nil
	}

	// The queue must still be the original one, without the provider_id
	// column the migration adds: then the rename never happened.
	var queues, providerColumns int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'notification_delivery_queue'`).Scan(&queues); err != nil {
		return err
	}
	if err := sqlDB.QueryRow(`SELECT count(*) FROM pragma_table_info('notification_delivery_queue') WHERE name = 'provider_id'`).Scan(&providerColumns); err != nil {
		return err
	}
	if queues != 1 || providerColumns != 0 {
		return nil
	}

	if _, err := sqlDB.Exec(`DROP TABLE IF EXISTS notification_delivery_queue_new; DROP TABLE IF EXISTS notification_providers`); err != nil {
		return err
	}
	if err := m.Force(42); err != nil {
		return err
	}
	log.Warn("repaired the database left dirty by migration 43 of v0.12.0; applying it again")
	return nil
}
