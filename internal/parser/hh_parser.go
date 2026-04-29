package parser

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"job-intel-bot/internal/domain"
)

const (
	hhAPIBase              = "https://api.hh.ru"
	maxConcurrentHHFetches = 8
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
	Specialization string // professional_role ID (e.g. "96")
	Employment     string // full | part | project | volunteer | probation
	AreaID         int    // HH area ID (0 = not set)
	SearchField    string // name | company_name | description (empty = all)
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

// Parse fetches the first page of results then enriches each job with its full description.
func (p *HHParser) Parse() ([]domain.Job, error) {
	vacancies, err := p.fetchList(0)
	if err != nil {
		return nil, fmt.Errorf("hh: fetch page 0: %w", err)
	}

	jobs := make([]domain.Job, len(vacancies))
	for i, v := range vacancies {
		jobs[i] = p.toJob(v)
	}

	log.Printf("hh: fetched %d vacancies, enriching descriptions", len(jobs))
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
	ID           string `json:"id"`
	Name         string `json:"name"`
	AlternateURL string `json:"alternate_url"`
	Employer     struct {
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
	Schedule struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"schedule"`
	Area struct {
		Name string `json:"name"`
	} `json:"area"`
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
	q.Set("per_page", "50")
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
	orderBy := p.filters.OrderBy
	if orderBy == "" {
		orderBy = "publication_time"
	}
	q.Set("order_by", orderBy)
	if p.filters.Specialization != "" {
		q.Set("professional_role", p.filters.Specialization)
	}
	if p.filters.Employment != "" {
		q.Set("employment", p.filters.Employment)
	}
	if p.filters.AreaID != 0 {
		q.Set("area", strconv.Itoa(p.filters.AreaID))
	}
	if p.filters.SearchField != "" {
		q.Set("search_field", p.filters.SearchField)
	}

	rawURL := hhAPIBase + "/vacancies?" + q.Encode()
	log.Printf("hh: GET %s", rawURL)
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
		Location:    v.Area.Name,
		WorkFormat:  v.Schedule.ID,
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
		// HH API returns dates as "2006-01-02T15:04:05+0700" (no colon in offset).
		// Try RFC3339 first, then the colon-less variant.
		t, err := time.Parse(time.RFC3339, v.PublishedAt)
		if err != nil {
			t, err = time.Parse("2006-01-02T15:04:05-0700", v.PublishedAt)
		}
		if err == nil {
			j.PostedAt = &t
		}
	}

	return j
}

// SearchArea finds the first HH area matching the query text using the suggests API.
// Returns (0, "", nil) when no match is found.
func SearchArea(client *http.Client, query string) (int, string, error) {
	rawURL := hhAPIBase + "/suggests/areas?text=" + url.QueryEscape(query)
	resp, err := client.Get(rawURL)
	if err != nil {
		return 0, "", fmt.Errorf("hh: search area: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Items []struct {
			ID   string `json:"id"`
			Text string `json:"text"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, "", fmt.Errorf("hh: decode area suggestions: %w", err)
	}
	if len(result.Items) == 0 {
		return 0, "", nil
	}

	id, err := strconv.Atoi(result.Items[0].ID)
	if err != nil {
		return 0, "", fmt.Errorf("hh: parse area id %q: %w", result.Items[0].ID, err)
	}
	return id, result.Items[0].Text, nil
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
