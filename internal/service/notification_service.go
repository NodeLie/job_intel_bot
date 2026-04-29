// Package service orchestrates fetching new vacancies and delivering notifications.
package service

import (
	"fmt"
	"log"

	"job-intel-bot/internal/domain"
)

// Parser fetches vacancies from an external source.
// Implemented by parser.HabrParser and parser.HHParser.
type Parser interface {
	Parse() ([]domain.Job, error)
}

// ParserFactory constructs a Parser for the given subscription.
type ParserFactory func(domain.Subscription) (Parser, error)

// notifier delivers vacancy notifications to a user.
type notifier interface {
	Notify(chatID int64, jobs []domain.Job) error
}

// seenRepository tracks which fingerprints were already sent to each user.
type seenRepository interface {
	IsSeen(userID int64, fingerprint string) (bool, error)
	MarkSeen(userID int64, fingerprint string) error
}

// NotificationService checks for new vacancies and sends notifications.
type NotificationService struct {
	seen    seenRepository
	factory ParserFactory
	notify  notifier
}

// NewNotificationService creates a NotificationService.
func NewNotificationService(seen seenRepository, factory ParserFactory, notify notifier) *NotificationService {
	return &NotificationService{
		seen:    seen,
		factory: factory,
		notify:  notify,
	}
}

// CheckAndNotify fetches new vacancies for the given subscription and notifies the user.
// Only jobs whose fingerprint was not seen before are delivered.
func (s *NotificationService) CheckAndNotify(sub domain.Subscription) error {
	log.Printf("service: sub %d user %d source %s query %q: checking", sub.ID, sub.UserID, sub.Source, sub.Keyword)
	p, err := s.factory(sub)
	if err != nil {
		return fmt.Errorf("service: build parser for sub %d: %w", sub.ID, err)
	}

	jobs, err := p.Parse()
	if err != nil {
		return fmt.Errorf("service: parse sub %d: %w", sub.ID, err)
	}

	var newJobs []domain.Job
	for _, j := range jobs {
		seen, err := s.seen.IsSeen(sub.UserID, j.Fingerprint)
		if err != nil {
			log.Printf("service: is_seen sub %d fp %s: %v", sub.ID, j.Fingerprint, err)
			continue
		}
		if seen {
			continue
		}
		newJobs = append(newJobs, j)
	}

	if len(newJobs) == 0 {
		log.Printf("service: sub %d user %d: no new jobs", sub.ID, sub.UserID)
		return nil
	}

	if err := s.notify.Notify(sub.UserID, newJobs); err != nil {
		return fmt.Errorf("service: notify sub %d: %w", sub.ID, err)
	}
	log.Printf("service: sub %d user %d: sent %d new jobs", sub.ID, sub.UserID, len(newJobs))

	// mark as seen only after successful delivery
	for _, j := range newJobs {
		if err := s.seen.MarkSeen(sub.UserID, j.Fingerprint); err != nil {
			log.Printf("service: mark_seen sub %d fp %s: %v", sub.ID, j.Fingerprint, err)
		}
	}

	return nil
}