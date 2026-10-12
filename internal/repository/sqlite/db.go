package sqlite

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
)

// unicode_lower заменяет встроенные LOWER() и LIKE: в SQLite без ICU они меняют регистр только у ASCII,
// и «Ошибка» не находилась по запросу «ошибка». Функция регистрируется до первого соединения.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("unicode_lower", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return strings.ToLower(v), nil
		case []byte:
			return strings.ToLower(string(v)), nil
		default:
			return v, nil
		}
	})
}

// InitDB initializes SQLite database and creates tables
func InitDB(dbPath string) (*sql.DB, error) {
	// Ensure directory exists for SQLite file
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create db directory %s: %w", dir, err)
		}
	}

	// PRAGMA действует на одно соединение, поэтому busy_timeout задаётся в DSN для всего пула:
	// без него параллельная запись (синхронизация GitHub vs приём событий) сразу падает с SQLITE_BUSY
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
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
		api_key TEXT NOT NULL UNIQUE,
		github_branch TEXT DEFAULT 'main',
		path_prefix TEXT,
		github_repo_id INTEGER
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
		code_snippet TEXT,
		php_version TEXT,
		laravel_version TEXT,
		route_name TEXT,
		route_action TEXT,
		execution_type TEXT DEFAULT 'http',
		system_context TEXT,
		request_context TEXT,
		queue_context TEXT,
		breadcrumbs TEXT,
		app_context TEXT,
		FOREIGN KEY(project_id) REFERENCES projects(id)
	);

	CREATE TABLE IF NOT EXISTS github_accounts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		login TEXT NOT NULL DEFAULT '',
		token TEXT NOT NULL,
		import_all INTEGER NOT NULL DEFAULT 1,
		synced_at DATETIME,
		sync_error TEXT NOT NULL DEFAULT ''
	);

	CREATE TABLE IF NOT EXISTS github_repos (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_id INTEGER NOT NULL,
		full_name TEXT NOT NULL,
		default_branch TEXT NOT NULL DEFAULT 'main',
		private INTEGER NOT NULL DEFAULT 0,
		selected INTEGER NOT NULL DEFAULT 0,
		UNIQUE(account_id, full_name)
	);
	`
	_, err = db.Exec(createBaseSchema)
	if err != nil {
		return nil, err
	}

	// 1. Idempotent migration for existing tables that may miss the new columns
	if err := migrateProjectsColumns(db); err != nil {
		return nil, fmt.Errorf("project migration failed: %w", err)
	}
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
	CREATE INDEX IF NOT EXISTS idx_incidents_execution ON incidents(project_id, execution_type);
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
		{"code_snippet", "TEXT"},
		{"php_version", "TEXT"},
		{"laravel_version", "TEXT"},
		{"route_name", "TEXT"},
		{"route_action", "TEXT"},
		{"execution_type", "TEXT DEFAULT 'http'"},
		{"system_context", "TEXT"},
		{"request_context", "TEXT"},
		{"queue_context", "TEXT"},
		{"breadcrumbs", "TEXT"},
		{"app_context", "TEXT"},
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

func migrateProjectsColumns(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(projects);")
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
		{"github_branch", "TEXT DEFAULT 'main'"},
		{"path_prefix", "TEXT"},
		{"github_repo_id", "INTEGER"},
	}

	for _, col := range columnsToAdd {
		if !existing[col.name] {
			stmt := fmt.Sprintf("ALTER TABLE projects ADD COLUMN %s %s;", col.name, col.def)
			if _, err := db.Exec(stmt); err != nil {
				return fmt.Errorf("failed adding column %s: %w", col.name, err)
			}
			log.Printf("[DB] Added column %s to projects", col.name)
		}
	}

	// Старые привязки сбрасываются намеренно: токены теперь живут в github_accounts
	for _, col := range []string{"github_repo", "github_token"} {
		if existing[col] {
			if _, err := db.Exec("ALTER TABLE projects DROP COLUMN " + col); err != nil {
				return fmt.Errorf("failed dropping column %s: %w", col, err)
			}
			log.Printf("[DB] Dropped column %s from projects", col)
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

