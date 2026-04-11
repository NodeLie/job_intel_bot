// Command bot starts the Telegram bot, connects to storage,
// launches the scheduler, and handles graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"job-intel-bot/internal/bot"
	"job-intel-bot/internal/domain"
	"job-intel-bot/internal/notifier"
	"job-intel-bot/internal/parser"
	"job-intel-bot/internal/repository"
	"job-intel-bot/internal/scheduler"
	"job-intel-bot/internal/service"
)

func main() {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Fatal("BOT_TOKEN environment variable is required")
	}

	subPath      := envOr("SUBS_PATH", "subscriptions.json")
	seenPath     := envOr("SEEN_PATH", "seen_jobs.json")
	pollInterval := parseDuration(envOr("POLL_INTERVAL", "30m"))

	// --- persistence ---
	repo, err := repository.NewJSONRepository(subPath, seenPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}

	// --- telegram ---
	botAPI, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("telegram auth: %v", err)
	}
	log.Printf("authorised as @%s", botAPI.Self.UserName)

	// --- wiring ---
	tgNotifier := notifier.NewTelegramNotifier(botAPI)
	svc := service.NewNotificationService(repo, buildParserFactory(), tgNotifier)

	sch := scheduler.New(repo, svc, pollInterval)
	sch.Start()
	defer sch.Stop()

	handler := bot.New(botAPI, repo)

	// --- update loop ---
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := botAPI.GetUpdatesChan(u)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("bot started (poll interval: %s)", pollInterval)

	for {
		select {
		case update := <-updates:
			go handler.Dispatch(update)
		case <-stop:
			log.Println("shutting down…")
			return
		}
	}
}

// buildParserFactory returns a service.ParserFactory that picks the correct
// parser implementation based on the subscription's source field.
func buildParserFactory() service.ParserFactory {
	client := &http.Client{Timeout: 15 * time.Second}

	return func(sub domain.Subscription) (service.Parser, error) {
		switch sub.Source {
		case domain.SourceHabr:
			return parser.NewHabrParser(client, sub.Keyword), nil
		case domain.SourceHH:
			f := sub.Filters
			return parser.NewHHParser(client, sub.Keyword, atLeastOne(f.Pages), parser.HHFilters{
				Experience:     f.Experience,
				WorkFormat:     f.WorkFormat,
				Salary:         f.Salary,
				CurrencyCode:   f.CurrencyCode,
				OnlyWithSalary: f.OnlyWithSalary,
				SearchPeriod:   f.SearchPeriod,
				OrderBy:        f.OrderBy,
			}), nil
		default:
			return nil, fmt.Errorf("unknown source: %q", sub.Source)
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDuration(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		log.Printf("invalid POLL_INTERVAL %q, using 30m", s)
		return 30 * time.Minute
	}
	return d
}

func atLeastOne(n int) int {
	if n > 0 {
		return n
	}
	return 1
}
