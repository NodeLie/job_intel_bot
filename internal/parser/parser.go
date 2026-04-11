// Package parser defines the interface for collecting vacancies from external sources
// and provides concrete implementations for Habr Career and HeadHunter.
package parser

import "job-intel-bot/internal/domain"

// JobParser fetches vacancies from a single external source.
type JobParser interface {
	Parse() ([]domain.Job, error)
}