// Package bot handles Telegram bot commands and dispatches them to the service layer.
package bot

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"job-intel-bot/internal/domain"
)

// subscriptionRepository is the persistence interface required by the handler.
type subscriptionRepository interface {
	Save(sub *domain.Subscription) error
	ListByUser(userID int64) ([]domain.Subscription, error)
	Delete(id int64) error
	SetActive(id int64, active bool) error
}

// Handler dispatches incoming Telegram commands.
type Handler struct {
	bot   *tgbotapi.BotAPI
	subs  subscriptionRepository
}

// New creates a Handler.
func New(bot *tgbotapi.BotAPI, subs subscriptionRepository) *Handler {
	return &Handler{bot: bot, subs: subs}
}

// Dispatch routes an incoming update to the appropriate command handler.
func (h *Handler) Dispatch(update tgbotapi.Update) {
	if update.Message == nil || !update.Message.IsCommand() {
		return
	}

	chatID := update.Message.Chat.ID
	args := strings.TrimSpace(update.Message.CommandArguments())

	switch update.Message.Command() {
	case "start":
		h.handleStart(chatID)
	case "help":
		h.handleHelp(chatID)
	case "add":
		h.handleAdd(chatID, args)
	case "list":
		h.handleList(chatID)
	case "remove":
		h.handleRemove(chatID, args)
	case "pause":
		h.handleSetActive(chatID, args, false)
	case "resume":
		h.handleSetActive(chatID, args, true)
	case "status":
		h.handleStatus(chatID)
	default:
		h.reply(chatID, "Неизвестная команда. Используй /help.")
	}
}

func (h *Handler) handleStart(chatID int64) {
	h.reply(chatID, "👋 Привет! Я слежу за вакансиями на Habr Career и HeadHunter.\n\n"+
		"Используй /add чтобы добавить подписку, /help чтобы увидеть все команды.")
}

func (h *Handler) handleHelp(chatID int64) {
	h.reply(chatID,
		"/add <ключевое слово> [habr|hh]  — добавить подписку\n"+
			"/list                           — список подписок\n"+
			"/remove <id>                    — удалить подписку\n"+
			"/pause <id>                     — приостановить\n"+
			"/resume <id>                    — возобновить\n"+
			"/status                         — сводка\n"+
			"/help                           — эта справка",
	)
}

// handleAdd parses "/add <keyword> [source]" and creates a new subscription.
// Examples:
//
//	/add Go разработчик
//	/add Go разработчик hh
func (h *Handler) handleAdd(chatID int64, args string) {
	if args == "" {
		h.reply(chatID, "Укажи ключевое слово. Пример: /add Go разработчик hh")
		return
	}

	parts := strings.Fields(args)
	source := domain.SourceHabr
	keyword := args

	// If the last token is a known source identifier, use it.
	if len(parts) > 1 {
		last := strings.ToLower(parts[len(parts)-1])
		switch last {
		case "hh":
			source = domain.SourceHH
			keyword = strings.Join(parts[:len(parts)-1], " ")
		case "habr":
			source = domain.SourceHabr
			keyword = strings.Join(parts[:len(parts)-1], " ")
		}
	}

	sub := &domain.Subscription{
		UserID:    chatID,
		Keyword:   keyword,
		Source:    source,
		Active:    true,
		CreatedAt: time.Now(),
	}

	if err := h.subs.Save(sub); err != nil {
		log.Printf("bot: save sub user %d: %v", chatID, err)
		h.reply(chatID, "Не удалось сохранить подписку, попробуй ещё раз.")
		return
	}

	h.reply(chatID, fmt.Sprintf(
		"✅ Подписка #%d добавлена:\n📌 «%s» на %s",
		sub.ID, sub.Keyword, sub.Source,
	))
}

func (h *Handler) handleList(chatID int64) {
	subs, err := h.subs.ListByUser(chatID)
	if err != nil {
		log.Printf("bot: list subs user %d: %v", chatID, err)
		h.reply(chatID, "Не удалось загрузить подписки.")
		return
	}

	if len(subs) == 0 {
		h.reply(chatID, "У тебя нет активных подписок. Используй /add чтобы добавить.")
		return
	}

	var b strings.Builder
	b.WriteString("📋 <b>Твои подписки:</b>\n\n")
	for _, s := range subs {
		status := "▶️"
		if !s.Active {
			status = "⏸"
		}
		fmt.Fprintf(&b, "%s #%d — «%s» (%s)\n", status, s.ID, s.Keyword, s.Source)
	}

	msg := tgbotapi.NewMessage(chatID, b.String())
	msg.ParseMode = tgbotapi.ModeHTML
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("bot: send list user %d: %v", chatID, err)
	}
}

func (h *Handler) handleRemove(chatID int64, args string) {
	id, ok := parseID(args)
	if !ok {
		h.reply(chatID, "Укажи ID подписки. Пример: /remove 3")
		return
	}

	if err := h.subs.Delete(id); err != nil {
		log.Printf("bot: delete sub %d user %d: %v", id, chatID, err)
		h.reply(chatID, "Не удалось удалить подписку.")
		return
	}

	h.reply(chatID, fmt.Sprintf("🗑 Подписка #%d удалена.", id))
}

func (h *Handler) handleSetActive(chatID int64, args string, active bool) {
	id, ok := parseID(args)
	if !ok {
		if active {
			h.reply(chatID, "Укажи ID подписки. Пример: /resume 3")
		} else {
			h.reply(chatID, "Укажи ID подписки. Пример: /pause 3")
		}
		return
	}

	if err := h.subs.SetActive(id, active); err != nil {
		log.Printf("bot: set_active sub %d user %d: %v", id, chatID, err)
		h.reply(chatID, "Не удалось обновить подписку.")
		return
	}

	if active {
		h.reply(chatID, fmt.Sprintf("▶️ Подписка #%d возобновлена.", id))
	} else {
		h.reply(chatID, fmt.Sprintf("⏸ Подписка #%d приостановлена.", id))
	}
}

func (h *Handler) handleStatus(chatID int64) {
	subs, err := h.subs.ListByUser(chatID)
	if err != nil {
		h.reply(chatID, "Не удалось получить статус.")
		return
	}

	active := 0
	for _, s := range subs {
		if s.Active {
			active++
		}
	}

	h.reply(chatID, fmt.Sprintf(
		"📊 Всего подписок: %d\nАктивных: %d\nПриостановленных: %d",
		len(subs), active, len(subs)-active,
	))
}

func (h *Handler) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("bot: reply to %d: %v", chatID, err)
	}
}

func parseID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}