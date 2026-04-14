// Package bot handles Telegram bot commands and dispatches them to the service layer.
package bot

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"job-intel-bot/internal/domain"
	"job-intel-bot/internal/parser"
)

// subscriptionRepository is the persistence interface required by the handler.
type subscriptionRepository interface {
	Save(sub *domain.Subscription) error
	ListByUser(userID int64) ([]domain.Subscription, error)
	Delete(id int64) error
	SetActive(id int64, active bool) error
}

// wizardStep identifies the current step in the /add conversation wizard.
type wizardStep int

const (
	stepSource         wizardStep = iota // choosing habr / hh
	stepHHSetup                          // setup filters or skip
	stepSpecialization                   // HH professional_role
	stepExperience                       // HH experience
	stepEmployment                       // HH employment type
	stepWorkFormat                       // HH work format
	stepRegion                           // HH region (awaits text input)
	stepSearchField                      // HH search field
)

// wizardState holds the in-progress /add or /edit subscription being built.
type wizardState struct {
	keyword  string
	filters  domain.Filter
	msgID    int                  // message ID to edit on each wizard step
	original *domain.Subscription // non-nil when editing; preserves ID, Active, CreatedAt
}

// hhSpecialization pairs a display label with a HH professional_role ID.
type hhSpecialization struct {
	Label string
	ID    string
}

// hhSpecializations is a curated list of IT-relevant HH professional roles.
// IDs verified from https://api.hh.ru/professional_roles.
var hhSpecializations = []hhSpecialization{
	{"💻 Разработка", "96"},
	{"🧪 Тестирование", "124"},
	{"⚙️ DevOps", "160"},
	{"🤖 Data Science", "165"},
	{"📊 Аналитика", "10"},
	{"🔐 Инфобезопасность", "116"},
	{"👥 Рук. разработки", "104"},
	{"📦 Менеджер продукта", "73"},
}

// Handler dispatches incoming Telegram commands and wizard callbacks.
type Handler struct {
	bot        *tgbotapi.BotAPI
	subs       subscriptionRepository
	httpClient *http.Client
	states     sync.Map // map[int64]*wizardState
}

// New creates a Handler. httpClient is used for HH area search requests.
func New(bot *tgbotapi.BotAPI, subs subscriptionRepository, httpClient *http.Client) *Handler {
	return &Handler{bot: bot, subs: subs, httpClient: httpClient}
}

// Dispatch routes an incoming update to the appropriate handler.
func (h *Handler) Dispatch(update tgbotapi.Update) {
	switch {
	case update.CallbackQuery != nil:
		h.handleCallback(update.CallbackQuery)

	case update.Message != nil:
		chatID := update.Message.Chat.ID
		// If we're waiting for a text region input, intercept the message.
		if v, ok := h.states.Load(chatID); ok {
			state := v.(*wizardState)
			if state.filters.AreaID == -1 { // sentinel: waiting for region text
				h.handleRegionInput(update.Message, state)
				return
			}
		}
		if update.Message.IsCommand() {
			h.handleCommand(update.Message)
		}
	}
}

func (h *Handler) handleCommand(msg *tgbotapi.Message) {
	chatID := msg.Chat.ID
	args := strings.TrimSpace(msg.CommandArguments())

	// Any new command cancels an in-progress wizard.
	h.states.Delete(chatID)

	switch msg.Command() {
	case "start":
		h.handleStart(chatID)
	case "help":
		h.handleHelp(chatID)
	case "add":
		h.handleAdd(chatID, args)
	case "edit":
		h.handleEdit(chatID, args)
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

// ── Wizard ──────────────────────────────────────────────────────────────────

// handleAdd starts the /add wizard by asking which source to use.
func (h *Handler) handleAdd(chatID int64, args string) {
	if args == "" {
		h.reply(chatID, "Укажи ключевое слово. Пример: /add Go разработчик")
		return
	}

	state := &wizardState{keyword: args}
	h.states.Store(chatID, state)

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔵 HeadHunter", "wiz:src:hh"),
			tgbotapi.NewInlineKeyboardButtonData("🟠 Habr Career", "wiz:src:habr"),
		),
	)
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Подписка на «<b>%s</b>»\n\nВыбери источник:", escapeHTML(args)))
	msg.ParseMode = tgbotapi.ModeHTML
	msg.ReplyMarkup = kb

	sent, err := h.bot.Send(msg)
	if err != nil {
		log.Printf("bot: add wizard start: %v", err)
		return
	}
	state.msgID = sent.MessageID
}

// handleCallback dispatches inline keyboard callbacks.
func (h *Handler) handleCallback(query *tgbotapi.CallbackQuery) {
	h.answerCallback(query.ID)

	chatID := query.Message.Chat.ID
	msgID := query.Message.MessageID
	data := query.Data

	// Subscription picker callbacks have the form "sel:<action>[:<id>]"
	if strings.HasPrefix(data, "sel:") {
		parts := strings.SplitN(data, ":", 3)
		if len(parts) < 2 {
			return
		}
		action := parts[1]
		switch action {
		case "cancel":
			h.editText(chatID, msgID, "Отменено.")
		case "edit":
			if len(parts) != 3 {
				return
			}
			id, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil || id <= 0 {
				return
			}
			h.selEdit(chatID, msgID, id)
		case "rm":
			if len(parts) != 3 {
				return
			}
			id, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil || id <= 0 {
				return
			}
			h.selRemove(chatID, msgID, id)
		case "rm_ok":
			if len(parts) != 3 {
				return
			}
			id, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil || id <= 0 {
				return
			}
			h.selRemoveOK(chatID, msgID, id)
		}
		return
	}

	// All wizard callbacks have the form "wiz:<field>:<value>"
	if !strings.HasPrefix(data, "wiz:") {
		return
	}
	parts := strings.SplitN(data, ":", 3)
	if len(parts) != 3 {
		return
	}
	field, value := parts[1], parts[2]

	v, ok := h.states.Load(chatID)
	if !ok {
		h.editText(chatID, msgID, "Сессия устарела. Начни заново с /add.")
		return
	}
	state := v.(*wizardState)
	state.msgID = msgID

	switch field {
	case "src":
		h.wizSource(chatID, state, value)
	case "setup":
		h.wizSetup(chatID, state, value)
	case "spec":
		h.wizSpecialization(chatID, state, value)
	case "exp":
		h.wizExperience(chatID, state, value)
	case "emp":
		h.wizEmployment(chatID, state, value)
	case "fmt":
		h.wizWorkFormat(chatID, state, value)
	case "reg":
		h.wizRegionSkip(chatID, state)
	case "sf":
		h.wizSearchField(chatID, state, value)
	}
}

func (h *Handler) wizSource(chatID int64, state *wizardState, value string) {
	switch value {
	case "habr":
		h.createSubscription(chatID, state, domain.SourceHabr)
	case "hh":
		h.editWithKeyboard(chatID, state.msgID,
			fmt.Sprintf("Подписка на «<b>%s</b>» (HH)\n\nНастроить фильтры?", escapeHTML(state.keyword)),
			tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("⚙️ Настроить фильтры", "wiz:setup:yes"),
					tgbotapi.NewInlineKeyboardButtonData("⚡ Создать без фильтров", "wiz:setup:no"),
				),
			),
		)
	}
}

func (h *Handler) wizSetup(chatID int64, state *wizardState, value string) {
	switch value {
	case "no":
		h.createSubscription(chatID, state, domain.SourceHH)
	case "cancel":
		h.states.Delete(chatID)
		h.editText(chatID, state.msgID, "✏️ Редактирование отменено.")
	default: // "yes"
		h.showSpecializationStep(chatID, state)
	}
}

func (h *Handler) showSpecializationStep(chatID int64, state *wizardState) {
	rows := [][]tgbotapi.InlineKeyboardButton{}
	for i := 0; i < len(hhSpecializations); i += 2 {
		row := []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(
				hhSpecializations[i].Label, "wiz:spec:"+hhSpecializations[i].ID),
		}
		if i+1 < len(hhSpecializations) {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(
				hhSpecializations[i+1].Label, "wiz:spec:"+hhSpecializations[i+1].ID))
		}
		rows = append(rows, row)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Пропустить →", "wiz:spec:skip"),
	))
	prompt := "Специализация:"
	if state.filters.Specialization != "" {
		prompt += "\nТекущая: " + specializationLabel(state.filters.Specialization)
	}
	h.editWithKeyboard(chatID, state.msgID, prompt, tgbotapi.NewInlineKeyboardMarkup(rows...))
}

func (h *Handler) wizSpecialization(chatID int64, state *wizardState, value string) {
	if value == "skip" {
		state.filters.Specialization = ""
	} else {
		state.filters.Specialization = value
	}
	prompt := "Опыт работы:"
	if state.filters.Experience != "" {
		prompt += "\nТекущий: " + experienceLabel(state.filters.Experience)
	}
	h.editWithKeyboard(chatID, state.msgID, prompt,
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Без опыта", "wiz:exp:noExperience"),
				tgbotapi.NewInlineKeyboardButtonData("1–3 года", "wiz:exp:between1And3"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("3–6 лет", "wiz:exp:between3And6"),
				tgbotapi.NewInlineKeyboardButtonData("6+ лет", "wiz:exp:moreThan6"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Пропустить →", "wiz:exp:skip"),
			),
		),
	)
}

func (h *Handler) wizExperience(chatID int64, state *wizardState, value string) {
	if value == "skip" {
		state.filters.Experience = ""
	} else {
		state.filters.Experience = value
	}
	prompt := "Тип занятости:"
	if state.filters.Employment != "" {
		prompt += "\nТекущий: " + employmentLabel(state.filters.Employment)
	}
	h.editWithKeyboard(chatID, state.msgID, prompt,
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Полная", "wiz:emp:full"),
				tgbotapi.NewInlineKeyboardButtonData("Частичная", "wiz:emp:part"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Проектная", "wiz:emp:project"),
				tgbotapi.NewInlineKeyboardButtonData("Стажировка", "wiz:emp:probation"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Пропустить →", "wiz:emp:skip"),
			),
		),
	)
}

func (h *Handler) wizEmployment(chatID int64, state *wizardState, value string) {
	if value == "skip" {
		state.filters.Employment = ""
	} else {
		state.filters.Employment = value
	}
	prompt := "Формат работы:"
	if state.filters.WorkFormat != "" {
		prompt += "\nТекущий: " + workFormatWizLabel(state.filters.WorkFormat)
	}
	h.editWithKeyboard(chatID, state.msgID, prompt,
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("💻 Удалённо", "wiz:fmt:REMOTE"),
				tgbotapi.NewInlineKeyboardButtonData("🏢 Офис", "wiz:fmt:ONSITE"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔀 Гибрид", "wiz:fmt:HYBRID"),
				tgbotapi.NewInlineKeyboardButtonData("Пропустить →", "wiz:fmt:skip"),
			),
		),
	)
}

func (h *Handler) wizWorkFormat(chatID int64, state *wizardState, value string) {
	if value == "skip" {
		state.filters.WorkFormat = ""
	} else {
		state.filters.WorkFormat = value
	}
	// Use sentinel -1 to signal "waiting for region text input"
	state.filters.AreaID = -1
	prompt := "Регион:\nВведи название города в чат или нажми «Пропустить»."
	if state.filters.AreaName != "" {
		prompt = fmt.Sprintf("Регион:\nТекущий: %s\nВведи новое название или нажми «Пропустить» чтобы сбросить.", escapeHTML(state.filters.AreaName))
	}
	h.editWithKeyboard(chatID, state.msgID, prompt,
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Пропустить →", "wiz:reg:skip"),
			),
		),
	)
}

func (h *Handler) wizRegionSkip(chatID int64, state *wizardState) {
	state.filters.AreaID = 0
	state.filters.AreaName = ""
	h.showSearchFieldStep(chatID, state)
}

func (h *Handler) handleRegionInput(msg *tgbotapi.Message, state *wizardState) {
	chatID := msg.Chat.ID
	query := strings.TrimSpace(msg.Text)

	// Delete the user's message to keep chat tidy (best-effort)
	h.bot.Request(tgbotapi.NewDeleteMessage(chatID, msg.MessageID)) //nolint:errcheck

	id, name, err := parser.SearchArea(h.httpClient, query)
	if err != nil {
		log.Printf("bot: search area %q: %v", query, err)
		h.editText(chatID, state.msgID, "Ошибка поиска региона, попробуй ещё раз или нажми Пропустить.")
		return
	}
	if id == 0 {
		h.editText(chatID, state.msgID,
			fmt.Sprintf("Регион «%s» не найден. Попробуй другое название или нажми Пропустить.",
				escapeHTML(query)))
		// Re-show skip button
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("Пропустить →", "wiz:reg:skip"),
			),
		)
		edit := tgbotapi.NewEditMessageReplyMarkup(chatID, state.msgID, kb)
		h.bot.Request(edit) //nolint:errcheck
		return
	}

	state.filters.AreaID = id
	state.filters.AreaName = name
	h.showSearchFieldStep(chatID, state)
}

func (h *Handler) showSearchFieldStep(chatID int64, state *wizardState) {
	prompt := "Где искать ключевые слова?"
	if state.filters.SearchField != "" {
		prompt += "\nТекущее: " + searchFieldLabel(state.filters.SearchField)
	}
	h.editWithKeyboard(chatID, state.msgID, prompt,
		tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("В названии вакансии", "wiz:sf:name"),
				tgbotapi.NewInlineKeyboardButtonData("В описании", "wiz:sf:description"),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("В названии компании", "wiz:sf:company_name"),
				tgbotapi.NewInlineKeyboardButtonData("Везде (по умолчанию)", "wiz:sf:skip"),
			),
		),
	)
}

func (h *Handler) wizSearchField(chatID int64, state *wizardState, value string) {
	if value == "skip" {
		state.filters.SearchField = ""
	} else {
		state.filters.SearchField = value
	}
	h.createSubscription(chatID, state, domain.SourceHH)
}

// createSubscription saves (or updates) the subscription and clears the wizard state.
func (h *Handler) createSubscription(chatID int64, state *wizardState, source domain.Source) {
	h.states.Delete(chatID)

	var sub domain.Subscription
	if state.original != nil {
		// Edit mode: preserve ID, UserID, Active, CreatedAt — only update filters.
		sub = *state.original
		sub.Filters = state.filters
	} else {
		sub = domain.Subscription{
			UserID:    chatID,
			Keyword:   state.keyword,
			Source:    source,
			Filters:   state.filters,
			Active:    true,
			CreatedAt: time.Now(),
		}
	}

	if err := h.subs.Save(&sub); err != nil {
		log.Printf("bot: save sub user %d: %v", chatID, err)
		h.editText(chatID, state.msgID, "Не удалось сохранить подписку, попробуй ещё раз.")
		return
	}

	var text string
	if state.original != nil {
		text = fmt.Sprintf("✅ Подписка #%d обновлена!", sub.ID)
	} else {
		text = fmt.Sprintf("✅ Подписка #%d добавлена!\n📌 «%s» на %s",
			sub.ID, escapeHTML(sub.Keyword), sub.Source)
	}
	h.editText(chatID, state.msgID, text)
}

// ── Commands ─────────────────────────────────────────────────────────────────

func (h *Handler) handleStart(chatID int64) {
	h.reply(chatID, "👋 Привет! Я слежу за вакансиями на Habr Career и HeadHunter.\n\n"+
		"Используй /add чтобы добавить подписку, /help чтобы увидеть все команды.")
}

func (h *Handler) handleHelp(chatID int64) {
	h.reply(chatID,
		"/add <ключевое слово>  — добавить подписку (запустит wizard выбора источника)\n"+
			"/edit                  — изменить фильтры подписки\n"+
			"/list                  — список подписок\n"+
			"/remove                — удалить подписку\n"+
			"/pause <id>            — приостановить\n"+
			"/resume <id>           — возобновить\n"+
			"/status                — сводка\n"+
			"/help                  — эта справка",
	)
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
		fmt.Fprintf(&b, "%s #%d — «%s» (%s)", status, s.ID, escapeHTML(s.Keyword), s.Source)
		if s.Filters.AreaName != "" {
			fmt.Fprintf(&b, " · 📍%s", escapeHTML(s.Filters.AreaName))
		}
		b.WriteByte('\n')
	}

	msg := tgbotapi.NewMessage(chatID, b.String())
	msg.ParseMode = tgbotapi.ModeHTML
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("bot: send list user %d: %v", chatID, err)
	}
}

func (h *Handler) handleRemove(chatID int64, _ string) {
	subs, err := h.subs.ListByUser(chatID)
	if err != nil {
		log.Printf("bot: remove list subs user %d: %v", chatID, err)
		h.reply(chatID, "Не удалось загрузить подписки.")
		return
	}
	if len(subs) == 0 {
		h.reply(chatID, "У тебя нет подписок.")
		return
	}

	msg := tgbotapi.NewMessage(chatID, "🗑 Выбери подписку для удаления:")
	msg.ReplyMarkup = buildSubPickerKeyboard(subs, "sel:rm")
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("bot: remove picker send user %d: %v", chatID, err)
	}
}

// selRemove is invoked when the user taps a subscription in the remove picker.
// It shows a confirmation dialog before deleting.
func (h *Handler) selRemove(chatID int64, msgID int, id int64) {
	subs, err := h.subs.ListByUser(chatID)
	if err != nil {
		log.Printf("bot: sel_remove list subs user %d: %v", chatID, err)
		h.editText(chatID, msgID, "Не удалось загрузить подписки.")
		return
	}

	var found *domain.Subscription
	for i := range subs {
		if subs[i].ID == id {
			found = &subs[i]
			break
		}
	}
	if found == nil {
		h.editText(chatID, msgID, fmt.Sprintf("Подписка #%d не найдена.", id))
		return
	}

	text := fmt.Sprintf("Удалить подписку #%d «<b>%s</b>»?", id, escapeHTML(found.Keyword))
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗑 Да, удалить", fmt.Sprintf("sel:rm_ok:%d", id)),
			tgbotapi.NewInlineKeyboardButtonData("↩️ Отмена", "sel:cancel"),
		),
	)
	h.editWithKeyboard(chatID, msgID, text, kb)
}

// selRemoveOK is invoked when the user confirms deletion.
func (h *Handler) selRemoveOK(chatID int64, msgID int, id int64) {
	// Verify ownership before deleting.
	subs, err := h.subs.ListByUser(chatID)
	if err != nil {
		log.Printf("bot: sel_remove_ok list subs user %d: %v", chatID, err)
		h.editText(chatID, msgID, "Не удалось загрузить подписки.")
		return
	}
	var owned bool
	for _, s := range subs {
		if s.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		h.editText(chatID, msgID, fmt.Sprintf("Подписка #%d не найдена.", id))
		return
	}

	if err := h.subs.Delete(id); err != nil {
		log.Printf("bot: sel_remove_ok delete sub %d user %d: %v", id, chatID, err)
		h.editText(chatID, msgID, "Не удалось удалить подписку.")
		return
	}
	h.editText(chatID, msgID, fmt.Sprintf("🗑 Подписка #%d удалена.", id))
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

func (h *Handler) handleEdit(chatID int64, _ string) {
	subs, err := h.subs.ListByUser(chatID)
	if err != nil {
		log.Printf("bot: edit list subs user %d: %v", chatID, err)
		h.reply(chatID, "Не удалось загрузить подписки.")
		return
	}
	if len(subs) == 0 {
		h.reply(chatID, "У тебя нет подписок.")
		return
	}

	msg := tgbotapi.NewMessage(chatID, "✏️ Выбери подписку для редактирования:")
	msg.ReplyMarkup = buildSubPickerKeyboard(subs, "sel:edit")
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("bot: edit picker send user %d: %v", chatID, err)
	}
}

// selEdit is invoked when the user taps a subscription in the edit picker.
func (h *Handler) selEdit(chatID int64, msgID int, id int64) {
	subs, err := h.subs.ListByUser(chatID)
	if err != nil {
		log.Printf("bot: sel_edit list subs user %d: %v", chatID, err)
		h.editText(chatID, msgID, "Не удалось загрузить подписки.")
		return
	}

	var found *domain.Subscription
	for i := range subs {
		if subs[i].ID == id {
			found = &subs[i]
			break
		}
	}
	if found == nil {
		h.editText(chatID, msgID, fmt.Sprintf("Подписка #%d не найдена.", id))
		return
	}
	if found.Source == domain.SourceHabr {
		h.editText(chatID, msgID, "У Habr-подписок нет настраиваемых фильтров.")
		return
	}

	state := &wizardState{
		keyword:  found.Keyword,
		filters:  found.Filters,
		original: found,
		msgID:    msgID,
	}
	h.states.Store(chatID, state)

	text := fmt.Sprintf(
		"✏️ Редактирование подписки #%d «<b>%s</b>» (HH)\n\nТекущие фильтры:\n%s\nПерейти к настройке:",
		id, escapeHTML(found.Keyword), formatFilters(found.Filters),
	)
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⚙️ Перенастроить фильтры", "wiz:setup:yes"),
			tgbotapi.NewInlineKeyboardButtonData("❌ Отмена", "wiz:setup:cancel"),
		),
	)
	h.editWithKeyboard(chatID, msgID, text, kb)
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

// ── Helpers ──────────────────────────────────────────────────────────────────

// buildSubPickerKeyboard builds an inline keyboard with one button per subscription.
// action is the callback prefix, e.g. "sel:edit" or "sel:rm".
func buildSubPickerKeyboard(subs []domain.Subscription, action string) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(subs))
	for _, s := range subs {
		icon := "▶️"
		if !s.Active {
			icon = "⏸"
		}
		label := fmt.Sprintf("%s #%d %s (%s)", icon, s.ID, s.Keyword, s.Source)
		cb := fmt.Sprintf("%s:%d", action, s.ID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, cb),
		))
	}
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func (h *Handler) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := h.bot.Send(msg); err != nil {
		log.Printf("bot: reply to %d: %v", chatID, err)
	}
}

func (h *Handler) editText(chatID int64, msgID int, text string) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = tgbotapi.ModeHTML
	if _, err := h.bot.Request(edit); err != nil {
		log.Printf("bot: edit message %d: %v", msgID, err)
	}
}

func (h *Handler) editWithKeyboard(chatID int64, msgID int, text string, kb tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ParseMode = tgbotapi.ModeHTML
	edit.ReplyMarkup = &kb
	if _, err := h.bot.Request(edit); err != nil {
		log.Printf("bot: edit message %d: %v", msgID, err)
	}
}

func (h *Handler) answerCallback(id string) {
	cb := tgbotapi.NewCallback(id, "")
	if _, err := h.bot.Request(cb); err != nil {
		log.Printf("bot: answer callback: %v", err)
	}
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func parseID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// formatFilters renders the non-zero fields of a Filter as a bullet list.
func formatFilters(f domain.Filter) string {
	var b strings.Builder
	if f.Specialization != "" {
		fmt.Fprintf(&b, "• Специализация: %s\n", specializationLabel(f.Specialization))
	}
	if f.Experience != "" {
		fmt.Fprintf(&b, "• Опыт: %s\n", experienceLabel(f.Experience))
	}
	if f.Employment != "" {
		fmt.Fprintf(&b, "• Занятость: %s\n", employmentLabel(f.Employment))
	}
	if f.WorkFormat != "" {
		fmt.Fprintf(&b, "• Формат: %s\n", workFormatWizLabel(f.WorkFormat))
	}
	if f.AreaName != "" {
		fmt.Fprintf(&b, "• Регион: %s\n", f.AreaName)
	}
	if f.SearchField != "" {
		fmt.Fprintf(&b, "• Поиск: %s\n", searchFieldLabel(f.SearchField))
	}
	if b.Len() == 0 {
		return "не заданы\n"
	}
	return b.String()
}

func specializationLabel(id string) string {
	for _, s := range hhSpecializations {
		if s.ID == id {
			return s.Label
		}
	}
	return id
}

func experienceLabel(v string) string {
	switch v {
	case "noExperience":
		return "Без опыта"
	case "between1And3":
		return "1–3 года"
	case "between3And6":
		return "3–6 лет"
	case "moreThan6":
		return "6+ лет"
	default:
		return v
	}
}

func employmentLabel(v string) string {
	switch v {
	case "full":
		return "Полная"
	case "part":
		return "Частичная"
	case "project":
		return "Проектная"
	case "probation":
		return "Стажировка"
	default:
		return v
	}
}

func workFormatWizLabel(v string) string {
	switch v {
	case "REMOTE":
		return "Удалённо"
	case "ONSITE":
		return "Офис"
	case "HYBRID":
		return "Гибрид"
	default:
		return v
	}
}

func searchFieldLabel(v string) string {
	switch v {
	case "name":
		return "В названии вакансии"
	case "description":
		return "В описании"
	case "company_name":
		return "В названии компании"
	default:
		return v
	}
}
