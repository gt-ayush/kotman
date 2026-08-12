package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func InitDB(filepath string) error {
	var err error
	DB, err = sql.Open("sqlite3", filepath)
	if err != nil {
		return err
	}

	// Create tables
	schema := `
	CREATE TABLE IF NOT EXISTS devices (
		device_id TEXT PRIMARY KEY,
		nickname TEXT UNIQUE NOT NULL,
		status TEXT NOT NULL DEFAULT 'OFFLINE',
		first_seen DATETIME DEFAULT CURRENT_TIMESTAMP,
		last_seen DATETIME,
		agent_version TEXT
	);
	CREATE TABLE IF NOT EXISTS audit_logs (
		log_id INTEGER PRIMARY KEY AUTOINCREMENT,
		device_id TEXT,
		operation TEXT,
		success BOOLEAN,
		error_msg TEXT,
		executed_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS sequence (
		name TEXT PRIMARY KEY,
		val INTEGER
	);
	INSERT INTO sequence (name, val) VALUES ('pc_counter', 0) ON CONFLICT DO NOTHING;
	`
	_, err = DB.Exec(schema)
	return err
}

// RegisterOrConnect returns the nickname of the device, creating it if it's new.
func RegisterOrConnect(deviceID, version string) (string, error) {
	tx, err := DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var nickname string
	err = tx.QueryRow("SELECT nickname FROM devices WHERE device_id = ?", deviceID).Scan(&nickname)
	
	now := time.Now().Format(time.RFC3339)

	if err == sql.ErrNoRows {
		// New Device: increment counter and assign default name
		_, err = tx.Exec("UPDATE sequence SET val = val + 1 WHERE name = 'pc_counter'")
		if err != nil { return "", err }

		var seq int
		tx.QueryRow("SELECT val FROM sequence WHERE name = 'pc_counter'").Scan(&seq)
		nickname = fmt.Sprintf("PC-%03d", seq)

		_, err = tx.Exec(`
			INSERT INTO devices (device_id, nickname, status, first_seen, last_seen, agent_version)
			VALUES (?, ?, 'ONLINE', ?, ?, ?)`,
			deviceID, nickname, now, now, version)
	} else {
		// Existing Device: mark ONLINE and update last seen
		_, err = tx.Exec(`
			UPDATE devices SET status = 'ONLINE', last_seen = ?, agent_version = ? 
			WHERE device_id = ?`, now, version, deviceID)
	}

	if err != nil { return "", err }
	return nickname, tx.Commit()
}

func LogOperation(deviceID, operation string, success bool, errorMsg string) {
	_, err := DB.Exec(`
		INSERT INTO audit_logs (device_id, operation, success, error_msg) 
		VALUES (?, ?, ?, ?)`, 
		deviceID, operation, success, errorMsg)
	if err != nil {
		// Log error internally, but don't crash the server
		fmt.Println("Audit log error:", err)
	}
}
