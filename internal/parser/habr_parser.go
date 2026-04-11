package parser

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"job-intel-bot/internal/domain"
)

const (
	habrBaseURL                  = "https://career.habr.com"
	habrVacanciesPath            = "/vacancies"
	maxConcurrentHabrFetches     = 8
)

// HabrParser fetches vacancies from career.habr.com.
type HabrParser struct {
	client *http.Client
	query  string
}

// NewHabrParser creates a HabrParser. If client is nil a default 15-second timeout client is used.
func NewHabrParser(client *http.Client, query string) *HabrParser {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &HabrParser{client: client, query: query}
}

// Parse fetches the listing page and then enriches each job with its description.
func (p *HabrParser) Parse() ([]domain.Job, error) {
	doc, err := p.fetchDoc(p.listURL())
	if err != nil {
		return nil, fmt.Errorf("habr: fetch listing: %w", err)
	}

	jobs := p.extractJobs(doc)
	if len(jobs) == 0 {
		return nil, nil
	}

	sem := make(chan struct{}, maxConcurrentHabrFetches)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := range jobs {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			desc, err := p.fetchDescription(jobs[idx].URL)
			if err != nil {
				// non-fatal: keep the job with an empty description
				return
			}
			mu.Lock()
			jobs[idx].Description = desc
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	return jobs, nil
}

func (p *HabrParser) listURL() string {
	u := habrBaseURL + habrVacanciesPath
	if p.query != "" {
		u += "?q=" + url.QueryEscape(p.query)
	}
	return u
}

func (p *HabrParser) fetchDoc(rawURL string) (*goquery.Document, error) {
	resp, err := p.client.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return goquery.NewDocumentFromReader(resp.Body)
}

func (p *HabrParser) fetchDescription(jobURL string) (string, error) {
	doc, err := p.fetchDoc(jobURL)
	if err != nil {
		return "", fmt.Errorf("habr: fetch %s: %w", jobURL, err)
	}
	return p.extractDescription(doc), nil
}

func (p *HabrParser) extractJobs(doc *goquery.Document) []domain.Job {
	var jobs []domain.Job

	doc.Find(".vacancy-card").Each(func(_ int, s *goquery.Selection) {
		titleSel := s.Find(".vacancy-card__title a")
		title := strings.TrimSpace(titleSel.Text())
		href, _ := titleSel.Attr("href")
		if title == "" || href == "" {
			return
		}
		jobURL := habrBaseURL + href
		company := strings.TrimSpace(s.Find(".vacancy-card__company-title").Text())

		jobs = append(jobs, domain.Job{
			Title:       title,
			Company:     company,
			URL:         jobURL,
			Source:      domain.SourceHabr,
			Fingerprint: fingerprint(title, company, jobURL),
			CollectedAt: time.Now(),
		})
	})

	return jobs
}

func (p *HabrParser) extractDescription(doc *goquery.Document) string {
	raw := doc.Find(".vacancy-description__text").Text()
	// collapse runs of whitespace to single spaces
	fields := strings.Fields(raw)
	return strings.Join(fields, " ")
}