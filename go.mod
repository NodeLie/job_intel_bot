module job-intel-bot

go 1.25.5

require (
	github.com/PuerkitoBio/goquery v1.12.0
	github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
	golang.org/x/net v0.52.0
	gopkg.in/yaml.v3 v3.0.1
)

require github.com/andybalholm/cascadia v1.3.3 // indirect

// modernc.org/sqlite is planned for the SQLite repository implementation.
// Run: go get modernc.org/sqlite@latest
// See internal/repository/sqlite_repository.go
