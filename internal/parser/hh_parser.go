package parser

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"job-intel-bot/internal/domain"
)

const (
	hhAPIBase                = "https://api.hh.ru"
	maxConcurrentHHFetches   = 8
)

// HHFilters holds optional search parameters for the HeadHunter API.
type HHFilters struct {
	Experience     string // noExperience | between1And3 | between3And6 | moreThan6
	WorkFormat     string // REMOTE | ONSITE | HYBRID
	Salary         int    // minimum salary (0 = not set)
	CurrencyCode   string // RUR | USD | EUR
	OnlyWithSalary bool
	SearchPeriod   int    // 0 | 1 | 3 | 7 | 14 | 30 days
	OrderBy        string // relevance | publication_time | salary_desc | salary_asc
}

// HHParser fetches vacancies from the HeadHunter public API.
type HHParser struct {
	client  *http.Client
	query   string
	pages   int
	filters HHFilters
}

// NewHHParser creates an HHParser. If client is nil a default 15-second timeout client is used.
func NewHHParser(client *http.Client, query string, pages int, filters HHFilters) *HHParser {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	if pages < 1 {
		pages = 1
	}
	return &HHParser{client: client, query: query, pages: pages, filters: filters}
}

// Parse fetches all configured pages then enriches each job with its full description.
func (p *HHParser) Parse() ([]domain.Job, error) {
	var vacancies []hhVacancy
	for page := 0; page < p.pages; page++ {
		batch, err := p.fetchList(page)
		if err != nil {
			return nil, fmt.Errorf("hh: fetch page %d: %w", page, err)
		}
		vacancies = append(vacancies, batch...)
	}

	jobs := make([]domain.Job, len(vacancies))
	for i, v := range vacancies {
		jobs[i] = p.toJob(v)
	}

	sem := make(chan struct{}, maxConcurrentHHFetches)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := range jobs {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			detail, err := p.fetchDetail(id)
			if err != nil {
				return
			}
			mu.Lock()
			jobs[idx].Description = stripHTML(detail.Description)
			if len(detail.KeySkills) > 0 {
				skills := make([]string, len(detail.KeySkills))
				for j, ks := range detail.KeySkills {
					skills[j] = ks.Name
				}
				jobs[idx].Skills = skills
			}
			mu.Unlock()
		}(i, vacancies[i].ID)
	}
	wg.Wait()

	return jobs, nil
}

// hhVacancy is the list-endpoint response shape.
type hhVacancy struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	AlternateURL string `json:"alternate_url"`
	Employer   struct {
		Name string `json:"name"`
	} `json:"employer"`
	Salary *struct {
		From     int    `json:"from"`
		To       int    `json:"to"`
		Currency string `json:"currency"`
	} `json:"salary"`
	Experience struct {
		ID string `json:"id"`
	} `json:"experience"`
	PublishedAt string `json:"published_at"`
}

// hhDetail is the detail-endpoint response shape.
type hhDetail struct {
	Description string `json:"description"`
	KeySkills   []struct {
		Name string `json:"name"`
	} `json:"key_skills"`
}

func (p *HHParser) fetchList(page int) ([]hhVacancy, error) {
	q := url.Values{}
	q.Set("text", p.query)
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", "20")
	if p.filters.Experience != "" {
		q.Set("experience", p.filters.Experience)
	}
	if p.filters.WorkFormat != "" {
		q.Set("work_format", p.filters.WorkFormat)
	}
	if p.filters.Salary > 0 {
		q.Set("salary", strconv.Itoa(p.filters.Salary))
	}
	if p.filters.CurrencyCode != "" {
		q.Set("currency", p.filters.CurrencyCode)
	}
	if p.filters.OnlyWithSalary {
		q.Set("only_with_salary", "true")
	}
	if p.filters.SearchPeriod > 0 {
		q.Set("search_period", strconv.Itoa(p.filters.SearchPeriod))
	}
	if p.filters.OrderBy != "" {
		q.Set("order_by", p.filters.OrderBy)
	}

	rawURL := hhAPIBase + "/vacancies?" + q.Encode()
	resp, err := p.client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Items []hhVacancy `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

func (p *HHParser) fetchDetail(id string) (*hhDetail, error) {
	rawURL := hhAPIBase + "/vacancies/" + id
	resp, err := p.client.Get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("hh: fetch detail %s: %w", id, err)
	}
	defer resp.Body.Close()

	var detail hhDetail
	if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("hh: decode detail %s: %w", id, err)
	}
	return &detail, nil
}

func (p *HHParser) toJob(v hhVacancy) domain.Job {
	j := domain.Job{
		SourceID:    v.ID,
		Title:       v.Name,
		Company:     v.Employer.Name,
		URL:         v.AlternateURL,
		Source:      domain.SourceHH,
		Level:       mapLevel(v.Experience.ID),
		CollectedAt: time.Now(),
	}
	j.Fingerprint = fingerprint(j.Title, j.Company, j.URL)

	if v.Salary != nil {
		j.Salary = &domain.Salary{
			Min:      v.Salary.From,
			Max:      v.Salary.To,
			Currency: v.Salary.Currency,
		}
	}

	if v.PublishedAt != "" {
		t, err := time.Parse(time.RFC3339, v.PublishedAt)
		if err == nil {
			j.PostedAt = &t
		}
	}

	return j
}

func mapLevel(experienceID string) domain.Level {
	switch experienceID {
	case "noExperience":
		return domain.LevelJunior
	case "between1And3":
		return domain.LevelMiddle
	case "between3And6", "moreThan6":
		return domain.LevelSenior
	default:
		return ""
	}
}

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	return html.UnescapeString(htmlTagRe.ReplaceAllString(s, " "))
}