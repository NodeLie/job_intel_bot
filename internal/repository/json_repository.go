package repository

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"job-intel-bot/internal/domain"
)

// JSONRepository implements SubscriptionRepository and SeenRepository
// using two JSON files on disk. It is the default storage backend.
// Replace with SQLiteRepository when persistent SQL queries are needed.
type JSONRepository struct {
	mu      sync.RWMutex
	subPath string
	seenPath string
	subs    []storedSubscription
	seen    map[seenKey]struct{}
	nextID  int64
}

type storedSubscription struct {
	ID        int64           `json:"id"`
	UserID    int64           `json:"user_id"`
	Keyword   string          `json:"keyword"`
	Source    string          `json:"source"`
	Filters   domain.Filter   `json:"filters"`
	Active    bool            `json:"active"`
	CreatedAt time.Time       `json:"created_at"`
}

type seenKey struct {
	UserID      int64
	Fingerprint string
}

type seenEntry struct {
	UserID      int64     `json:"user_id"`
	Fingerprint string    `json:"fingerprint"`
	SeenAt      time.Time `json:"seen_at"`
}

// NewJSONRepository loads (or creates) the JSON store files at the given paths.
func NewJSONRepository(subPath, seenPath string) (*JSONRepository, error) {
	r := &JSONRepository{
		subPath:  subPath,
		seenPath: seenPath,
		seen:     make(map[seenKey]struct{}),
		nextID:   1,
	}

	if err := r.loadSubs(); err != nil {
		return nil, fmt.Errorf("json_repo: load subs: %w", err)
	}
	if err := r.loadSeen(); err != nil {
		return nil, fmt.Errorf("json_repo: load seen: %w", err)
	}
	return r, nil
}

// --- SubscriptionRepository ---

func (r *JSONRepository) Save(sub *domain.Subscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if sub.ID == 0 {
		sub.ID = r.nextID
		r.nextID++
		r.subs = append(r.subs, toStored(sub))
	} else {
		for i, s := range r.subs {
			if s.ID == sub.ID {
				r.subs[i] = toStored(sub)
				break
			}
		}
	}

	return r.saveSubs()
}

func (r *JSONRepository) ListByUser(userID int64) ([]domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []domain.Subscription
	for _, s := range r.subs {
		if s.UserID == userID {
			out = append(out, toDomain(s))
		}
	}
	return out, nil
}

func (r *JSONRepository) ListActive() ([]domain.Subscription, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out []domain.Subscription
	for _, s := range r.subs {
		if s.Active {
			out = append(out, toDomain(s))
		}
	}
	return out, nil
}

func (r *JSONRepository) Delete(id int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := r.subs[:0]
	for _, s := range r.subs {
		if s.ID != id {
			n = append(n, s)
		}
	}
	r.subs = n
	return r.saveSubs()
}

func (r *JSONRepository) SetActive(id int64, active bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, s := range r.subs {
		if s.ID == id {
			r.subs[i].Active = active
			break
		}
	}
	return r.saveSubs()
}

// --- SeenRepository ---

func (r *JSONRepository) IsSeen(userID int64, fingerprint string) (bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.seen[seenKey{userID, fingerprint}]
	return ok, nil
}

func (r *JSONRepository) MarkSeen(userID int64, fingerprint string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen[seenKey{userID, fingerprint}] = struct{}{}
	return r.saveSeen()
}

// --- disk I/O ---

func (r *JSONRepository) loadSubs() error {
	data, err := os.ReadFile(r.subPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &r.subs); err != nil {
		return err
	}
	for _, s := range r.subs {
		if s.ID >= r.nextID {
			r.nextID = s.ID + 1
		}
	}
	return nil
}

func (r *JSONRepository) saveSubs() error {
	data, err := json.MarshalIndent(r.subs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.subPath, data, 0o600)
}

func (r *JSONRepository) loadSeen() error {
	data, err := os.ReadFile(r.seenPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var entries []seenEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return err
	}
	for _, e := range entries {
		r.seen[seenKey{e.UserID, e.Fingerprint}] = struct{}{}
	}
	return nil
}

func (r *JSONRepository) saveSeen() error {
	entries := make([]seenEntry, 0, len(r.seen))
	for k := range r.seen {
		entries = append(entries, seenEntry{
			UserID:      k.UserID,
			Fingerprint: k.Fingerprint,
			SeenAt:      time.Now(),
		})
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.seenPath, data, 0o600)
}

// --- converters ---

func toStored(sub *domain.Subscription) storedSubscription {
	return storedSubscription{
		ID:        sub.ID,
		UserID:    sub.UserID,
		Keyword:   sub.Keyword,
		Source:    string(sub.Source),
		Filters:   sub.Filters,
		Active:    sub.Active,
		CreatedAt: sub.CreatedAt,
	}
}

func toDomain(s storedSubscription) domain.Subscription {
	return domain.Subscription{
		ID:        s.ID,
		UserID:    s.UserID,
		Keyword:   s.Keyword,
		Source:    domain.Source(s.Source),
		Filters:   s.Filters,
		Active:    s.Active,
		CreatedAt: s.CreatedAt,
	}
}