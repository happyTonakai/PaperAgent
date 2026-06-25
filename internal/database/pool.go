// Package database provides a SQLite connection pool for the recommendation system.
// Uses modernc.org/sqlite, a pure-Go (no CGO) SQLite implementation.
// The database is stored at ~/.config/paperagent/zenflow.db, separate from
// the existing JSON-based paper session storage.
package database

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/happyTonakai/paperagent/internal/config"
)

var (
	once   sync.Once
	db     *sql.DB
	dbErr  error
	testDB atomicDB // overrides GetDB when set (for tests)
)

// atomicDB wraps a *sql.DB with atomic load/store for thread safety.
type atomicDB struct {
	mu  sync.RWMutex
	ptr *sql.DB
}

func (a *atomicDB) load() *sql.DB {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ptr
}

func (a *atomicDB) store(d *sql.DB) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ptr = d
}

// SetDB injects a database connection for testing.
// Pass nil to reset. Safe for concurrent use.
func SetDB(d *sql.DB) {
	testDB.store(d)
}

// OpenTestDB opens an in-memory SQLite database and runs migrations.
// Each call creates an independent database (randomized URI to avoid sharing).
// Returns the connection and a cleanup function. Used in tests.
func OpenTestDB() (*sql.DB, func(), error) {
	// Generate unique DB name to avoid cross-test sharing with cache=shared
	var buf [8]byte
	rand.Read(buf[:])
	name := fmt.Sprintf("file:test_%x?mode=memory&cache=shared", buf[:])

	conn, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, nil, fmt.Errorf("open test db: %w", err)
	}
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("migrate test db: %w", err)
	}
	return conn, func() { conn.Close() }, nil
}

// DBPath returns the path to the recommendation SQLite database.
func DBPath() string {
	return filepath.Join(config.ConfigDir(), "zenflow.db")
}

// Open opens (or reuses) the SQLite database connection.
// The connection is lazily initialized on first call.
func Open() (*sql.DB, error) {
	once.Do(func() {
		dir := config.ConfigDir()
		if err := os.MkdirAll(dir, 0755); err != nil {
			dbErr = fmt.Errorf("create config dir: %w", err)
			return
		}

		path := DBPath()
		conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
		if err != nil {
			dbErr = fmt.Errorf("open sqlite: %w", err)
			return
		}

		// Configure pool
		conn.SetMaxOpenConns(1) // SQLite only supports one writer
		conn.SetMaxIdleConns(1)

		db = conn

		// Run migrations
		if err := migrate(conn); err != nil {
			dbErr = fmt.Errorf("migrate: %w", err)
			return
		}
	})

	return db, dbErr
}

// Close closes the database connection. Called during application shutdown.
func Close() error {
	if db != nil {
		return db.Close()
	}
	return nil
}

// GetDB returns the database connection, opening it if necessary.
// If SetDB was called (for tests), returns that instead.
func GetDB() (*sql.DB, error) {
	if d := testDB.load(); d != nil {
		return d, nil
	}
	return Open()
}

// migrate runs schema migrations. Uses a simple version table.
func migrate(conn *sql.DB) error {
	// Create migration tracking table
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS _migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create migrations table: %w", err)
	}

	// Get current version
	var currentVersion int
	err := conn.QueryRow("SELECT COALESCE(MAX(version), 0) FROM _migrations").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("get current migration version: %w", err)
	}

	// Apply migrations in order
	migrations := []struct {
		version int
		sql     string
	}{
		{1, schemaV1},
		{2, schemaV2},
		{3, schemaV3},
		{4, schemaV4},
		{5, schemaV5},
		{6, schemaV6},
		{7, schemaV7},
	}

	for _, m := range migrations {
		if m.version <= currentVersion {
			continue
		}
		if err := applyMigration(conn, m.version, m.sql); err != nil {
			return fmt.Errorf("migration v%d: %w", m.version, err)
		}
	}

	return nil
}

func applyMigration(conn *sql.DB, version int, sql string) error {
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(sql); err != nil {
		return fmt.Errorf("exec: %w", err)
	}

	if _, err := tx.Exec("INSERT INTO _migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}

	return tx.Commit()
}
