package search

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The search query is tenant-critical: company_id must always be a hard
// filter, never an optional should-clause.
func TestBuildSearchQuery(t *testing.T) {
	companyID := uuid.New()
	q := BuildSearchQuery(companyID, "cola", 20)

	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("marshal query: %v", err)
	}
	body := string(raw)

	if !strings.Contains(body, `"filter":[{"term":{"company_id":"`+companyID.String()+`"}}]`) {
		t.Errorf("company_id filter missing or not a hard filter:\n%s", body)
	}
	if !strings.Contains(body, `"fuzziness":"AUTO"`) {
		t.Errorf("name match must be fuzzy:\n%s", body)
	}
	if !strings.Contains(body, `"minimum_should_match":1`) {
		t.Errorf("at least one field must match:\n%s", body)
	}
	for _, field := range []string{`"sku":"cola"`, `"barcode":"cola"`, `"slot":"cola"`} {
		if !strings.Contains(body, field) {
			t.Errorf("exact term for %s missing:\n%s", field, body)
		}
	}
	if !strings.Contains(body, `"size":20`) {
		t.Errorf("size missing:\n%s", body)
	}
}
