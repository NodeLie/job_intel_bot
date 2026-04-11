// Package notifier formats and delivers vacancy notifications via Telegram.
package notifier

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"job-intel-bot/internal/domain"
)

// TelegramNotifier sends formatted job notifications to a Telegram chat.
type TelegramNotifier struct {
	bot *tgbotapi.BotAPI
}

// NewTelegramNotifier creates a notifier using the provided BotAPI instance.
func NewTelegramNotifier(bot *tgbotapi.BotAPI) *TelegramNotifier {
	return &TelegramNotifier{bot: bot}
}

// Notify sends one message per job to the given chat ID.
// It does not stop on individual send errors; all errors are collected and returned.
func (n *TelegramNotifier) Notify(chatID int64, jobs []domain.Job) error {
	var errs []string
	for _, j := range jobs {
		text := formatJob(j)
		msg := tgbotapi.NewMessage(chatID, text)
		msg.ParseMode = tgbotapi.ModeHTML
		msg.DisableWebPagePreview = true

		if _, err := n.bot.Send(msg); err != nil {
			errs = append(errs, fmt.Sprintf("send to %d: %v", chatID, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("notifier: %s", strings.Join(errs, "; "))
	}
	return nil
}

// formatJob renders a domain.Job as an HTML Telegram message.
func formatJob(j domain.Job) string {
	var b strings.Builder

	fmt.Fprintf(&b, "🆕 <b>%s</b>\n", escapeHTML(j.Title))
	if j.Company != "" {
		fmt.Fprintf(&b, "🏢 %s\n", escapeHTML(j.Company))
	}
	if j.Salary != nil {
		fmt.Fprintf(&b, "💰 %s\n", formatSalary(j.Salary))
	}
	if j.Level != "" {
		fmt.Fprintf(&b, "📊 %s\n", j.Level)
	}
	if len(j.Skills) > 0 {
		fmt.Fprintf(&b, "🛠 %s\n", strings.Join(j.Skills, ", "))
	}
	fmt.Fprintf(&b, "🔗 <a href=%q>Открыть вакансию</a>", j.URL)

	return b.String()
}

func formatSalary(s *domain.Salary) string {
	switch {
	case s.Min > 0 && s.Max > 0:
		return fmt.Sprintf("%d – %d %s", s.Min, s.Max, s.Currency)
	case s.Min > 0:
		return fmt.Sprintf("от %d %s", s.Min, s.Currency)
	case s.Max > 0:
		return fmt.Sprintf("до %d %s", s.Max, s.Currency)
	default:
		return "не указана"
	}
}

// escapeHTML escapes the minimal set of characters that break Telegram HTML mode.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}