// Package domain defines the core types shared across all layers of the application.
// It has no dependencies on other internal packages or external libraries.
package domain

import "time"

// Level represents the seniority level of a job posting.
type Level string

const (
	LevelJunior Level = "junior"
	LevelMiddle Level = "middle"
	LevelSenior Level = "senior"
)

// Source identifies the platform a vacancy was collected from.
type Source string

const (
	SourceHabr     Source = "habr"
	SourceHH       Source = "hh"
	SourceLinkedIn Source = "linkedin"
)

// Salary holds the compensation range for a vacancy.
// All three fields belong together; stored as a pointer in Job so it can be nil.
type Salary struct {
	Min      int
	Max      int
	Currency string // ISO 4217 code: RUB, USD, EUR
}

// Job represents a single vacancy collected from an external source.
type Job struct {
	ID          string
	SourceID    string     // native ID on the source platform
	Title       string
	Company     string
	Salary      *Salary    // nil when not specified
	Description string
	Skills      []string
	Level       Level
	Location    string     // city/region, empty when not provided by the source
	WorkFormat  string     // HH schedule ID: remote|fullDay|flexible|shift|flyInFlyOut
	Source      Source
	URL         string
	Fingerprint string     // 16-char SHA-256 hex prefix, used for deduplication
	PostedAt    *time.Time // nil when the source does not expose a posting date
	CollectedAt time.Time
}