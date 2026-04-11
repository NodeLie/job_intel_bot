// Package repository defines persistence interfaces and their SQLite implementations.
package repository

import (
	"job-intel-bot/internal/domain"
)

// SubscriptionRepository manages user subscriptions.
type SubscriptionRepository interface {
	// Save creates or updates a subscription. Sets ID on a new record.
	Save(sub *domain.Subscription) error
	// ListByUser returns all subscriptions for the given Telegram chat ID.
	ListByUser(userID int64) ([]domain.Subscription, error)
	// ListActive returns all subscriptions with Active = true.
	ListActive() ([]domain.Subscription, error)
	// Delete removes a subscription by ID.
	Delete(id int64) error
	// SetActive updates the Active flag on a subscription.
	SetActive(id int64, active bool) error
}

// SeenRepository tracks which job fingerprints have already been sent to a user.
type SeenRepository interface {
	// IsSeen reports whether the given fingerprint was already sent to userID.
	IsSeen(userID int64, fingerprint string) (bool, error)
	// MarkSeen records that fingerprint was sent to userID.
	MarkSeen(userID int64, fingerprint string) error
}