package parser

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"job-intel-bot/internal/domain"
)

const (
	hhScrapeBase           = "https://hh.ru"
	maxConcurrentHHScrapes = 8
	hhScrapeUserAgent      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
)

// HHScraper fetches vacancies from hh.ru via HTML scraping.
// It reuses HHFilters from hh_parser.go (same package).
type HHScraper struct {
	client  *http.Client
	query   string
	pages   int
	filters HHFilters
}

// NewHHScraper creates an HHScraper. If client is nil a default 15-second timeout client is used.
func NewHHScraper(client *http.Client, query string, pages int, filters HHFilters) *HHScraper {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if pages < 1 {
		pages = 1
	}
	return &HHScraper{client: client, query: query, pages: pages, filters: filters}
}

// Parse fetches search result pages then enriches each job with detail page data.
func (s *HHScraper) Parse() ([]domain.Job, error) {
	var items []hhListItem
	for page := 0; page < s.pages; page++ {
		pageItems, err := s.fetchListPage(page)
		if err != nil {
			return nil, fmt.Errorf("hh scraper: page %d: %w", page, err)
		}
		if len(pageItems) == 0 {
			break
		}
		items = append(items, pageItems...)
	}

	now := time.Now()
	jobs := make([]domain.Job, len(items))
	for i, item := range items {
		canonicalURL := hhScrapeBase + "/vacancy/" + item.vacID
		j := domain.Job{
			SourceID:    item.vacID,
			Title:       item.title,
			Company:     item.company,
			URL:         canonicalURL,
			Source:      domain.SourceHH,
			Level:       item.level,
			Location:    item.location,
			Salary:      item.salary,
			PostedAt:    item.postedAt,
			CollectedAt: now,
		}
		j.Fingerprint = fingerprint(j.Title, j.Company, j.URL)
		jobs[i] = j
	}

	log.Printf("hh scraper: fetched %d vacancies, enriching details", len(jobs))

	sem := make(chan struct{}, maxConcurrentHHScrapes)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := range jobs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			detail, err := s.fetchDetail(jobs[idx].URL)
			if err != nil {
				log.Printf("hh scraper: detail %s: %v", jobs[idx].URL, err)
				return
			}
			mu.Lock()
			jobs[idx].Description = detail.description
			jobs[idx].Skills = detail.skills
			if detail.salary != nil {
				jobs[idx].Salary = detail.salary
			}
			if detail.postedAt != nil {
				jobs[idx].PostedAt = detail.postedAt
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	return jobs, nil
}

type hhListItem struct {
	title    string
	company  string
	location string
	vacID    string
	level    domain.Level
	salary   *domain.Salary
	postedAt *time.Time
}

func (s *HHScraper) fetchListPage(page int) ([]hhListItem, error) {
	q := url.Values{}
	q.Set("text", s.query)
	q.Set("page", strconv.Itoa(page))
	q.Set("items_on_page", "50")
	if s.filters.SearchField != "" {
		q.Set("search_field", s.filters.SearchField)
	}
	if s.filters.Specialization != "" {
		q.Set("professional_role", s.filters.Specialization)
	}
	if s.filters.AreaID != 0 {
		q.Set("area", strconv.Itoa(s.filters.AreaID))
	}
	if s.filters.Experience != "" {
		q.Set("experience", s.filters.Experience)
	}
	if s.filters.WorkFormat != "" {
		q.Set("work_format", s.filters.WorkFormat)
	}
	if s.filters.Salary > 0 {
		q.Set("salary", strconv.Itoa(s.filters.Salary))
	}
	if s.filters.CurrencyCode != "" {
		q.Set("currency_code", s.filters.CurrencyCode)
	}
	if s.filters.OnlyWithSalary {
		q.Set("only_with_salary", "true")
	}
	if s.filters.SearchPeriod > 0 {
		q.Set("search_period", strconv.Itoa(s.filters.SearchPeriod))
	}
	orderBy := s.filters.OrderBy
	if orderBy == "" {
		orderBy = "publication_time"
	}
	q.Set("order_by", orderBy)
	// employment_form on the web takes uppercase values (FULL, PART, …)
	if s.filters.Employment != "" {
		q.Set("employment_form", strings.ToUpper(s.filters.Employment))
	}

	rawURL := hhScrapeBase + "/search/vacancy?" + q.Encode()
	log.Printf("hh scraper: GET %s", rawURL)

	doc, err := s.getDoc(rawURL)
	if err != nil {
		return nil, err
	}

	var items []hhListItem
	doc.Find("[data-qa='vacancy-serp__vacancy']").Each(func(_ int, card *goquery.Selection) {
		title := strings.TrimSpace(card.Find("[data-qa='serp-item__title-text']").Text())
		company := strings.TrimSpace(card.Find("[data-qa='vacancy-serp__vacancy-employer-text']").Text())
		location := strings.TrimSpace(card.Find("[data-qa='vacancy-serp__vacancy-address']").First().Text())
		salaryText := normalizeSpaces(card.Find("[data-qa='vacancy-serp__vacancy-compensation']").First().Text())

		href, exists := card.Find("[data-qa='serp-item__title']").Attr("href")
		if !exists || title == "" {
			return
		}
		vacID := extractVacancyID(href)
		if vacID == "" {
			return
		}

		var level domain.Level
		card.Find("[data-qa^='vacancy-serp__vacancy-work-experience-']").Each(func(_ int, el *goquery.Selection) {
			qa, _ := el.Attr("data-qa")
			expID := strings.TrimPrefix(qa, "vacancy-serp__vacancy-work-experience-")
			level = mapLevel(expID)
		})

		items = append(items, hhListItem{
			title:    title,
			company:  company,
			location: location,
			vacID:    vacID,
			level:    level,
			salary:   parseSalary(salaryText),
		})
	})

	return items, nil
}

type hhScrapedDetail struct {
	description string
	skills      []string
	salary      *domain.Salary
	postedAt    *time.Time
}

func (s *HHScraper) fetchDetail(vacURL string) (*hhScrapedDetail, error) {
	doc, err := s.getDoc(vacURL)
	if err != nil {
		return nil, err
	}

	description := strings.TrimSpace(doc.Find("[data-qa='vacancy-description']").Text())

	var skills []string
	doc.Find("[data-qa='bloko-tag__text']").Each(func(_ int, el *goquery.Selection) {
		skill := strings.TrimSpace(el.Text())
		if skill != "" {
			skills = append(skills, skill)
		}
	})

	salaryText := normalizeSpaces(doc.Find("[data-qa='vacancy-salary']").Text())
	salary := parseSalary(salaryText)
	postedAt := parsePostedAt(doc)

	return &hhScrapedDetail{
		description: description,
		skills:      skills,
		salary:      salary,
		postedAt:    postedAt,
	}, nil
}

func (s *HHScraper) getDoc(rawURL string) (*goquery.Document, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", hhScrapeUserAgent)
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	return goquery.NewDocumentFromReader(resp.Body)
}

var datePostedRe = regexp.MustCompile(`"datePosted"\s*:\s*"([^"]+)"`)

func parsePostedAt(doc *goquery.Document) *time.Time {
	for _, sel := range []string{
		"meta[itemprop='datePosted']",
		"meta[property='article:published_time']",
	} {
		node := doc.Find(sel).First()
		if val, ok := node.Attr("content"); ok {
			if t := parsePostedAtValue(val); t != nil {
				return t
			}
		}
	}

	if val, ok := doc.Find("time[datetime]").First().Attr("datetime"); ok {
		if t := parsePostedAtValue(val); t != nil {
			return t
		}
	}

	var postedAt *time.Time
	doc.Find("script[type='application/ld+json']").EachWithBreak(func(_ int, script *goquery.Selection) bool {
		if t := parsePostedAtFromJSONLD(script.Text()); t != nil {
			postedAt = t
			return false
		}
		return true
	})
	return postedAt
}

func parsePostedAtFromJSONLD(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var payload any
	if err := json.Unmarshal([]byte(raw), &payload); err == nil {
		if t := findDatePosted(payload); t != nil {
			return t
		}
	}

	match := datePostedRe.FindStringSubmatch(raw)
	if len(match) == 2 {
		return parsePostedAtValue(match[1])
	}
	return nil
}

func findDatePosted(v any) *time.Time {
	switch value := v.(type) {
	case map[string]any:
		if posted, ok := value["datePosted"].(string); ok {
			if t := parsePostedAtValue(posted); t != nil {
				return t
			}
		}
		for _, nested := range value {
			if t := findDatePosted(nested); t != nil {
				return t
			}
		}
	case []any:
		for _, nested := range value {
			if t := findDatePosted(nested); t != nil {
				return t
			}
		}
	}
	return nil
}

func parsePostedAtValue(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	for _, layout := range []string{
		time.RFC3339,
		"2006-01-02T15:04:05-0700",
		"2006-01-02",
	} {
		if t, err := time.Parse(layout, raw); err == nil {
			return &t
		}
	}
	return nil
}

// extractVacancyID extracts the numeric vacancy ID from an HH vacancy URL.
// e.g. "https://magnitogorsk.hh.ru/vacancy/131540785?query=go" → "131540785"
func extractVacancyID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, p := range parts {
		if p == "vacancy" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}

// parseSalary parses an HH salary string such as
// "до 325 000 ₽ за месяц до вычета налогов" or "от 200 000 до 300 000 ₽ за месяц"
// into a domain.Salary. Returns nil when the text cannot be parsed.
func parseSalary(text string) *domain.Salary {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var currency string
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(text, "₽") || strings.Contains(lower, "руб"):
		currency = "RUR"
	case strings.Contains(text, "$") || strings.Contains(lower, "usd"):
		currency = "USD"
	case strings.Contains(text, "€") || strings.Contains(lower, "eur"):
		currency = "EUR"
	}

	nums := extractSalaryNumbers(text)
	if len(nums) == 0 {
		return nil
	}

	hasOt := strings.Contains(lower, "от")
	// "до вычета налогов" contains "до" but is not a salary ceiling — exclude it
	// by checking whether "до" precedes a digit in the text.
	hasDo := hasSalaryCeiling(lower)

	sal := &domain.Salary{Currency: currency}
	switch {
	case hasOt && hasDo && len(nums) >= 2:
		sal.Min = nums[0]
		sal.Max = nums[1]
	case hasOt:
		sal.Min = nums[0]
	case hasDo:
		sal.Max = nums[0]
	default:
		sal.Min = nums[0]
		if len(nums) >= 2 {
			sal.Max = nums[1]
		}
	}
	return sal
}

// hasSalaryCeiling returns true when the text contains "до" immediately before a number,
// indicating a salary ceiling rather than "до вычета налогов" (before-tax note).
func hasSalaryCeiling(lower string) bool {
	const marker = "до "
	idx := 0
	for {
		pos := strings.Index(lower[idx:], marker)
		if pos < 0 {
			return false
		}
		pos += idx
		after := strings.TrimSpace(lower[pos+len(marker):])
		if len(after) > 0 && unicode.IsDigit(rune(after[0])) {
			return true
		}
		idx = pos + len(marker)
		if idx >= len(lower) {
			return false
		}
	}
}

// extractSalaryNumbers collects salary amounts from space-formatted Russian number strings
// like "от 200 000 до 300 000 ₽" by joining adjacent digit-only tokens.
func extractSalaryNumbers(text string) []int {
	var result []int
	var acc strings.Builder
	for _, tok := range strings.Fields(text) {
		if isDigitOnly(tok) {
			acc.WriteString(tok)
		} else if acc.Len() > 0 {
			if v, err := strconv.Atoi(acc.String()); err == nil && v > 0 {
				result = append(result, v)
			}
			acc.Reset()
		}
	}
	if acc.Len() > 0 {
		if v, err := strconv.Atoi(acc.String()); err == nil && v > 0 {
			result = append(result, v)
		}
	}
	return result
}

func isDigitOnly(s string) bool {
	if len(s) == 0 {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// normalizeSpaces replaces Unicode non-breaking and thin spaces with regular spaces.
func normalizeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case ' ', ' ', ' ', ' ':
			return ' '
		}
		return r
	}, s)
}
