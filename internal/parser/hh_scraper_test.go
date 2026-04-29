package parser

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestHHScraperParsePreservesListSalaryAndPublicationDate(t *testing.T) {
	t.Parallel()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			body := ""
			switch req.URL.Path {
			case "/search/vacancy":
				body = `
<html><body>
	<div data-qa="vacancy-serp__vacancy">
		<a data-qa="serp-item__title" href="https://hh.ru/vacancy/123456?from=serp">
			<span data-qa="serp-item__title-text">Go Developer</span>
		</a>
		<div data-qa="vacancy-serp__vacancy-employer-text">ACME</div>
		<div data-qa="vacancy-serp__vacancy-address">Remote</div>
		<div data-qa="vacancy-serp__vacancy-compensation">from 2000 USD</div>
		<div data-qa="vacancy-serp__vacancy-work-experience-between1And3"></div>
	</div>
</body></html>`
			case "/vacancy/123456":
				body = `
<html><head>
	<script type="application/ld+json">
		{"@context":"https://schema.org","datePosted":"2026-04-28T10:30:00+03:00"}
	</script>
</head><body>
	<div data-qa="vacancy-description">Build services</div>
	<span data-qa="bloko-tag__text">Go</span>
</body></html>`
			default:
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    req,
			}, nil
		}),
	}

	scraper := NewHHScraper(client, "golang", 1, HHFilters{})

	jobs, err := scraper.Parse()
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	job := jobs[0]
	if job.Salary == nil {
		t.Fatal("expected salary from list page to be preserved")
	}
	if job.Salary.Min != 2000 || job.Salary.Currency != "USD" {
		t.Fatalf("unexpected salary: %+v", *job.Salary)
	}
	if job.PostedAt == nil {
		t.Fatal("expected posted date to be set from detail page")
	}

	wantPostedAt := time.Date(2026, 4, 28, 10, 30, 0, 0, time.FixedZone("+0300", 3*60*60))
	if !job.PostedAt.Equal(wantPostedAt) {
		t.Fatalf("unexpected posted date: got %s want %s", job.PostedAt.Format(time.RFC3339), wantPostedAt.Format(time.RFC3339))
	}
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
