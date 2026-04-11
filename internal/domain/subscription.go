package domain

import "time"

// Subscription represents a user's saved search configuration.
// Each subscription maps to one periodic fetch + notify cycle.
type Subscription struct {
	ID        int64
	UserID    int64  // Telegram chat ID
	Keyword   string
	Source    Source
	Filters   Filter
	Active    bool
	CreatedAt time.Time
}

// Filter holds optional search parameters.
// Most fields are HH-specific; Habr uses only Keyword.
type Filter struct {
	Pages          int    // hh-only: number of result pages to fetch (default 1, max 5)
	Experience     string // hh-only: noExperience | between1And3 | between3And6 | moreThan6
	WorkFormat     string // hh-only: REMOTE | ONSITE | HYBRID
	Salary         int    // hh-only: minimum salary in CurrencyCode (0 = not set)
	CurrencyCode   string // hh-only: RUR | USD | EUR
	OnlyWithSalary bool   // hh-only: exclude vacancies without stated salary
	SearchPeriod   int    // hh-only: 0 | 1 | 3 | 7 | 14 | 30 days
	OrderBy        string // hh-only: relevance | publication_time | salary_desc | salary_asc
}