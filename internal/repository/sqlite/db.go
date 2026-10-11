package sqlite

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// InitDB initializes SQLite database and creates tables
func InitDB(dbPath string) (*sql.DB, error) {
	// Ensure directory exists for SQLite file
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec("PRAGMA journal_mode=WAL;")
	if err != nil {
		return nil, err
	}

	createBaseSchema := `
	CREATE TABLE IF NOT EXISTS projects (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		api_key TEXT NOT NULL UNIQUE
	);

	CREATE TABLE IF NOT EXISTS releases (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		version TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		UNIQUE(project_id, version),
		FOREIGN KEY(project_id) REFERENCES projects(id)
	);

	CREATE INDEX IF NOT EXISTS idx_releases_proj_date ON releases(project_id, created_at DESC);

	CREATE TABLE IF NOT EXISTS incidents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		project_id INTEGER NOT NULL,
		hash TEXT NOT NULL UNIQUE,
		exception_class TEXT NOT NULL,
		message TEXT NOT NULL,
		file TEXT NOT NULL,
		line INTEGER NOT NULL,
		stack_trace TEXT NOT NULL,
		app_url TEXT NOT NULL,
		request_url TEXT NOT NULL,
		events_count INTEGER NOT NULL DEFAULT 1,
		first_seen_at DATETIME NOT NULL,
		last_seen_at DATETIME NOT NULL,
		environment TEXT NOT NULL DEFAULT 'production',
		first_release TEXT,
		last_release TEXT,
		status TEXT NOT NULL DEFAULT 'unresolved',
		resolved_in_release TEXT,
		resolved_at DATETIME,
		is_regression INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY(project_id) REFERENCES projects(id)
	);
	`
	_, err = db.Exec(createBaseSchema)
	if err != nil {
		return nil, err
	}

	// 1. Idempotent migration for existing tables that may miss the new columns
	if err := migrateIncidentsColumns(db); err != nil {
		return nil, fmt.Errorf("incident migration failed: %w", err)
	}
	if err := migrateReleasesColumns(db); err != nil {
		return nil, fmt.Errorf("release migration failed: %w", err)
	}

	// 2. Create indexes ONLY AFTER columns are guaranteed to exist
	createIndexes := `
	CREATE INDEX IF NOT EXISTS idx_incidents_status ON incidents(project_id, status);
	CREATE INDEX IF NOT EXISTS idx_incidents_last_seen ON incidents(project_id, last_seen_at DESC);
	`
	if _, err := db.Exec(createIndexes); err != nil {
		return nil, fmt.Errorf("failed to create indexes: %w", err)
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count)
	if err == nil && count == 0 {
		_, _ = db.Exec("INSERT INTO projects (name, api_key) VALUES (?, ?)", "Default Project", "test-api-key")
		log.Println("Created default project with api_key: test-api-key")
	}

	return db, nil
}

func migrateIncidentsColumns(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(incidents);")
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}

	columnsToAdd := []struct {
		name string
		def  string
	}{
		{"environment", "TEXT NOT NULL DEFAULT 'production'"},
		{"first_release", "TEXT"},
		{"last_release", "TEXT"},
		{"status", "TEXT NOT NULL DEFAULT 'unresolved'"},
		{"resolved_in_release", "TEXT"},
		{"resolved_at", "DATETIME"},
		{"is_regression", "INTEGER NOT NULL DEFAULT 0"},
	}

	for _, col := range columnsToAdd {
		if !existing[col.name] {
			stmt := fmt.Sprintf("ALTER TABLE incidents ADD COLUMN %s %s;", col.name, col.def)
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("failed adding column %s: %w", col.name, err)
			}
			log.Printf("[DB] Added column %s to incidents", col.name)
		}
	}

	return nil
}

func migrateReleasesColumns(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(releases);")
	if err != nil {
		return err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dfltValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			return err
		}
		existing[name] = true
	}

	columnsToAdd := []struct {
		name string
		def  string
	}{
		{"ref", "TEXT"},
		{"commit_sha", "TEXT"},
	}

	for _, col := range columnsToAdd {
		if !existing[col.name] {
			stmt := fmt.Sprintf("ALTER TABLE releases ADD COLUMN %s %s;", col.name, col.def)
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("failed adding column %s: %w", col.name, err)
			}
			log.Printf("[DB] Added column %s to releases", col.name)
		}
	}

	return nil
}

