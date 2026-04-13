// Package notifier formats and delivers vacancy notifications via Telegram.
package notifier

import (
	"fmt"
	"strings"
	"time"

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

// Notify sends jobs to the given chat in batches of up to 10 per message.
func (n *TelegramNotifier) Notify(chatID int64, jobs []domain.Job) error {
	const batchSize = 10
	var errs []string

	for i := 0; i < len(jobs); i += batchSize {
		end := i + batchSize
		if end > len(jobs) {
			end = len(jobs)
		}
		text := formatBatch(jobs[i:end], i)

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

// formatBatch renders a slice of jobs as a single HTML message.
// startIdx is the global offset used for numbering (0-based).
func formatBatch(jobs []domain.Job, startIdx int) string {
	var parts []string
	for i, j := range jobs {
		parts = append(parts, formatJob(startIdx+i+1, j))
	}
	return strings.Join(parts, "\n\n")
}

// formatJob renders one vacancy as a compact HTML block.
func formatJob(n int, j domain.Job) string {
	var b strings.Builder

	// Title + level
	fmt.Fprintf(&b, "<b>%d. %s</b>", n, escapeHTML(j.Title))
	if j.Level != "" {
		fmt.Fprintf(&b, " · %s", levelLabel(j.Level))
	}
	b.WriteByte('\n')

	// Company / location / work format — all on one line if present
	var meta []string
	if j.Company != "" {
		meta = append(meta, "🏢 "+escapeHTML(j.Company))
	}
	if j.Location != "" {
		meta = append(meta, "📍 "+escapeHTML(j.Location))
	}
	if j.WorkFormat != "" {
		meta = append(meta, "💼 "+workFormatLabel(j.WorkFormat))
	}
	if len(meta) > 0 {
		b.WriteString(strings.Join(meta, " · "))
		b.WriteByte('\n')
	}

	// Salary
	if j.Salary != nil {
		fmt.Fprintf(&b, "💰 %s\n", formatSalary(j.Salary))
	} else {
		b.WriteString("💰 не указана\n")
	}

	// Publication date
	if j.PostedAt != nil {
		fmt.Fprintf(&b, "📅 %s\n", j.PostedAt.In(time.UTC).Format("02.01.2006"))
	}

	// Skills (first 5)
	if len(j.Skills) > 0 {
		skills := j.Skills
		if len(skills) > 5 {
			skills = skills[:5]
		}
		fmt.Fprintf(&b, "🔧 %s\n", strings.Join(skills, ", "))
	} else {
		b.WriteString("🔧 не указаны\n")
	}

	fmt.Fprintf(&b, `<a href=%q>Открыть вакансию →</a>`, j.URL)

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

func levelLabel(l domain.Level) string {
	switch l {
	case domain.LevelJunior:
		return "Junior"
	case domain.LevelMiddle:
		return "Middle"
	case domain.LevelSenior:
		return "Senior"
	default:
		return string(l)
	}
}

func workFormatLabel(wf string) string {
	switch wf {
	case "remote":
		return "Удалённо"
	case "fullDay":
		return "Офис"
	case "flexible":
		return "Гибрид"
	case "shift":
		return "Сменный"
	case "flyInFlyOut":
		return "Вахта"
	default:
		return wf
	}
}

// escapeHTML escapes the minimal set of characters that break Telegram HTML mode.
func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
