// Package scheduler runs periodic checks for all active subscriptions.
package scheduler

import (
	"log"
	"sync"
	"time"

	"job-intel-bot/internal/domain"
)

// subscriptionLister returns all active subscriptions.
type subscriptionLister interface {
	ListActive() ([]domain.Subscription, error)
}

// checker runs the check-and-notify cycle for a single subscription.
type checker interface {
	CheckAndNotify(sub domain.Subscription) error
}

// Scheduler ticks at a fixed interval and runs CheckAndNotify for every active subscription.
type Scheduler struct {
	subs     subscriptionLister
	checker  checker
	interval time.Duration
	stop     chan struct{}
	wg       sync.WaitGroup
}

// New creates a Scheduler that fires every interval.
func New(subs subscriptionLister, checker checker, interval time.Duration) *Scheduler {
	return &Scheduler{
		subs:     subs,
		checker:  checker,
		interval: interval,
		stop:     make(chan struct{}),
	}
}

// Start launches the background polling loop. It is safe to call once.
func (s *Scheduler) Start() {
	s.wg.Add(1)
	go s.loop()
}

// Stop signals the polling loop to exit and waits for it to finish.
func (s *Scheduler) Stop() {
	close(s.stop)
	s.wg.Wait()
}

func (s *Scheduler) loop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// run immediately on first tick so we don't wait a full interval at startup
	s.runAll()

	for {
		select {
		case <-ticker.C:
			s.runAll()
		case <-s.stop:
			return
		}
	}
}

func (s *Scheduler) runAll() {
	subs, err := s.subs.ListActive()
	if err != nil {
		log.Printf("scheduler: list active: %v", err)
		return
	}

	for _, sub := range subs {
		go func(sub domain.Subscription) {
			if err := s.checker.CheckAndNotify(sub); err != nil {
				log.Printf("scheduler: sub %d user %d: %v", sub.ID, sub.UserID, err)
			}
		}(sub)
	}
}