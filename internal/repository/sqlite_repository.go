// Package repository provides persistence implementations.
// SQLite implementation is planned for production use.
// See json_repository.go for the current in-use implementation.
package repository

// This file is intentionally left as a stub.
// SQLite implementation will be added in a follow-up task.
// Required dependency: modernc.org/sqlite (pure Go, no CGO)
//
// Schema (for future reference):
//
//	CREATE TABLE subscriptions (
//	    id         INTEGER PRIMARY KEY AUTOINCREMENT,
//	    user_id    INTEGER NOT NULL,
//	    keyword    TEXT    NOT NULL,
//	    source     TEXT    NOT NULL,
//	    filters    TEXT    NOT NULL DEFAULT '{}',
//	    active     INTEGER NOT NULL DEFAULT 1,
//	    created_at DATETIME NOT NULL
//	);
//
//	CREATE TABLE seen_jobs (
//	    fingerprint TEXT    NOT NULL,
//	    user_id     INTEGER NOT NULL,
//	    seen_at     DATETIME NOT NULL,
//	    PRIMARY KEY (fingerprint, user_id)
//	);