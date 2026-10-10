package sqlite

import (
	"database/sql"
	"log"

	_ "modernc.org/sqlite"
)

// InitDB initializes SQLite database and creates tables
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec("PRAGMA journal_mode=WAL;")
	if err != nil {
		return nil, err
	}

	createSchema := `
	CREATE TABLE IF NOT EXISTS projects (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		api_key TEXT NOT NULL UNIQUE
	);

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
		FOREIGN KEY(project_id) REFERENCES projects(id)
	);
	`
	_, err = db.Exec(createSchema)
	if err != nil {
		return nil, err
	}
	
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM projects").Scan(&count)
	if err == nil && count == 0 {
		_, _ = db.Exec("INSERT INTO projects (name, api_key) VALUES (?, ?)", "Default Project", "test-api-key")
		log.Println("Created default project with api_key: test-api-key")
	}

	return db, nil
}
