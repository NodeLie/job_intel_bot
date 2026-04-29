// Command bot starts the Telegram bot, connects to storage,
// launches the scheduler, and handles graceful shutdown on SIGINT/SIGTERM.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"job-intel-bot/internal/bot"
	"job-intel-bot/internal/config"
	"job-intel-bot/internal/domain"
	"job-intel-bot/internal/notifier"
	"job-intel-bot/internal/parser"
	"job-intel-bot/internal/repository"
	"job-intel-bot/internal/scheduler"
	"job-intel-bot/internal/service"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/net/proxy"
)

func main() {
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// --- persistence ---
	repo, err := repository.NewJSONRepository(cfg.SubsPath, cfg.SeenPath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}

	// --- telegram ---
	tgClient, err := buildTelegramClient(cfg.ProxyURL)
	if err != nil {
		log.Fatalf("proxy: %v", err)
	}
	botAPI, err := tgbotapi.NewBotAPIWithClient(cfg.BotToken, tgbotapi.APIEndpoint, tgClient)
	if err != nil {
		log.Fatalf("telegram auth: %v", err)
	}
	log.Printf("authorised as @%s", botAPI.Self.UserName)

	// --- register command menu ---
	registerCommands(botAPI)

	// --- wiring ---
	tgNotifier := notifier.NewTelegramNotifier(botAPI)
	svc := service.NewNotificationService(repo, buildParserFactory(), tgNotifier)

	sch := scheduler.New(repo, svc, cfg.PollIntervalDuration)
	sch.Start()
	defer sch.Stop()

	httpClient := &http.Client{Timeout: 15 * time.Second}
	handler := bot.New(botAPI, repo, httpClient)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-stop
		log.Println("shutting down…")
		cancel()
	}()

	log.Printf("bot started (poll interval: %s)", cfg.PollIntervalDuration)
	pollUpdates(ctx, botAPI, handler)
}

func registerCommands(botAPI *tgbotapi.BotAPI) {
	commands := []tgbotapi.BotCommand{
		{Command: "add", Description: "Добавить подписку"},
		{Command: "edit", Description: "Изменить фильтры подписки"},
		{Command: "list", Description: "Мои подписки"},
		{Command: "remove", Description: "Удалить подписку"},
		{Command: "pause", Description: "Приостановить подписку"},
		{Command: "resume", Description: "Возобновить подписку"},
		{Command: "status", Description: "Статистика"},
		{Command: "help", Description: "Справка"},
	}
	if _, err := botAPI.Request(tgbotapi.NewSetMyCommands(commands...)); err != nil {
		log.Printf("set commands: %v", err)
	}
}

// pollUpdates runs the Telegram long-poll loop with exponential backoff on errors.
// It exits when ctx is cancelled.
func pollUpdates(ctx context.Context, botAPI *tgbotapi.BotAPI, handler *bot.Handler) {
	offset := 0
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}

		u := tgbotapi.NewUpdate(offset)
		u.Timeout = 60

		updates, err := botAPI.GetUpdates(u)
		if err != nil {
			log.Printf("poll: %v (retry in %s)", err, backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 60*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second // reset on success

		for _, update := range updates {
			offset = update.UpdateID + 1
			go handler.Dispatch(update)
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
			return parser.NewHHScraper(client, sub.Keyword, atLeastOne(f.Pages), parser.HHFilters{
				Experience:     f.Experience,
				WorkFormat:     f.WorkFormat,
				Salary:         f.Salary,
				CurrencyCode:   f.CurrencyCode,
				OnlyWithSalary: f.OnlyWithSalary,
				SearchPeriod:   f.SearchPeriod,
				OrderBy:        f.OrderBy,
				Specialization: f.Specialization,
				Employment:     f.Employment,
				AreaID:         f.AreaID,
				SearchField:    f.SearchField,
			}), nil
		default:
			return nil, fmt.Errorf("unknown source: %q", sub.Source)
		}
	}
}

// buildTelegramClient returns an HTTP client configured with the given proxy URL.
// Supported schemes: http, https, socks5. Empty proxyURL returns a plain client.
func buildTelegramClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return &http.Client{}, nil
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy_url %q: %w", proxyURL, err)
	}
	switch u.Scheme {
	case "http", "https":
		return &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(u)}}, nil
	case "socks5":
		dialer, err := proxy.FromURL(u, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy_url %q: %w", proxyURL, err)
		}
		return &http.Client{Transport: &http.Transport{Dial: dialer.Dial}}, nil //nolint:staticcheck
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (supported: http, https, socks5)", u.Scheme)
	}
}

func atLeastOne(n int) int {
	if n > 0 {
		return n
	}
	return 1
}
