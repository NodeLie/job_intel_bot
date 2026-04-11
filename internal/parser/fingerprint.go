package parser

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// fingerprint returns a 16-char hex string derived from the SHA-256 hash of
// "title|company|url". It is used as a deduplication key across fetches.
func fingerprint(title, company, url string) string {
	key := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(title)),
		strings.ToLower(strings.TrimSpace(company)),
		strings.ToLower(strings.TrimSpace(url)),
	}, "|")
	sum := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", sum[:8]) // 16 hex chars
}