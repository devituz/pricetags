package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"
	"github.com/google/uuid"

	"pricetags/internal/domain"
)

const IndexName = "products"

type ES struct {
	client *elasticsearch.Client
	log    *slog.Logger
}

func New(url string, log *slog.Logger) (*ES, error) {
	client, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{url}})
	if err != nil {
		return nil, fmt.Errorf("elasticsearch client: %w", err)
	}
	return &ES{client: client, log: log}, nil
}

// EnsureIndex creates the products index with an explicit mapping if it
// does not exist yet.
func (e *ES) EnsureIndex(ctx context.Context) error {
	res, err := e.client.Indices.Exists([]string{IndexName}, e.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("check index: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == 200 {
		return nil
	}

	const mapping = `{
		"mappings": {
			"properties": {
				"id":           {"type": "keyword"},
				"company_id":   {"type": "keyword"},
				"name":         {"type": "text"},
				"sku":          {"type": "keyword"},
				"barcode":      {"type": "keyword"},
				"retail_price": {"type": "double"},
				"slot":         {"type": "keyword"}
			}
		}
	}`
	cres, err := e.client.Indices.Create(IndexName,
		e.client.Indices.Create.WithContext(ctx),
		e.client.Indices.Create.WithBody(strings.NewReader(mapping)))
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	defer cres.Body.Close()
	if cres.IsError() {
		body, _ := io.ReadAll(cres.Body)
		return fmt.Errorf("create index: %s", body)
	}
	return nil
}

// ProductDoc is the partial document written on product upsert.
// The slot field is intentionally absent: it is owned by slot updates
// and must survive product re-indexing (doc_as_upsert merge).
type ProductDoc struct {
	ID          string   `json:"id"`
	CompanyID   string   `json:"company_id"`
	Name        string   `json:"name"`
	SKU         string   `json:"sku"`
	Barcode     []string `json:"barcode"`
	RetailPrice float64  `json:"retail_price"`
}

// UpsertProducts merges product fields into their documents via _bulk.
func (e *ES) UpsertProducts(ctx context.Context, docs map[uuid.UUID]ProductDoc) error {
	if len(docs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for id, doc := range docs {
		meta := fmt.Sprintf(`{"update":{"_index":%q,"_id":%q}}`, IndexName, id.String())
		body, err := json.Marshal(map[string]any{"doc": doc, "doc_as_upsert": true})
		if err != nil {
			return fmt.Errorf("marshal product doc: %w", err)
		}
		buf.WriteString(meta)
		buf.WriteByte('\n')
		buf.Write(body)
		buf.WriteByte('\n')
	}
	return e.bulk(ctx, &buf)
}

// UpdateSlots rewrites only the slot field of each document via _bulk,
// leaving the rest of the document untouched.
func (e *ES) UpdateSlots(ctx context.Context, slots map[uuid.UUID][]string) error {
	if len(slots) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for id, s := range slots {
		if s == nil {
			s = []string{}
		}
		meta := fmt.Sprintf(`{"update":{"_index":%q,"_id":%q}}`, IndexName, id.String())
		body, err := json.Marshal(map[string]any{"doc": map[string]any{"slot": s}, "doc_as_upsert": true})
		if err != nil {
			return fmt.Errorf("marshal slot doc: %w", err)
		}
		buf.WriteString(meta)
		buf.WriteByte('\n')
		buf.Write(body)
		buf.WriteByte('\n')
	}
	return e.bulk(ctx, &buf)
}

// DeleteProducts removes documents of soft-deleted products via _bulk.
func (e *ES) DeleteProducts(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, id := range ids {
		fmt.Fprintf(&buf, "{\"delete\":{\"_index\":%q,\"_id\":%q}}\n", IndexName, id.String())
	}
	return e.bulk(ctx, &buf)
}

func (e *ES) bulk(ctx context.Context, buf *bytes.Buffer) error {
	res, err := e.client.Bulk(bytes.NewReader(buf.Bytes()),
		e.client.Bulk.WithContext(ctx),
		// wait_for makes writes visible to the next search; acceptable
		// for this service's write volume, revisit under heavy load.
		e.client.Bulk.WithRefresh("wait_for"))
	if err != nil {
		return fmt.Errorf("bulk request: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("bulk request: %s", body)
	}

	var parsed struct {
		Errors bool `json:"errors"`
		Items  []map[string]struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return fmt.Errorf("decode bulk response: %w", err)
	}
	if parsed.Errors {
		for _, item := range parsed.Items {
			for op, r := range item {
				// Deleting a document that was never indexed is fine.
				if op == "delete" && r.Status == 404 {
					continue
				}
				if r.Status >= 300 {
					return fmt.Errorf("bulk %s failed with status %d: %s", op, r.Status, r.Error)
				}
			}
		}
	}
	return nil
}

// BuildSearchQuery returns the ES query for GET /products/search:
// tenant filter is mandatory; name matches with fuzziness, sku/barcode/slot
// match exactly.
func BuildSearchQuery(companyID uuid.UUID, q string, limit int) map[string]any {
	return map[string]any{
		"size": limit,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"company_id": companyID.String()}},
				},
				"must": []any{
					map[string]any{"bool": map[string]any{
						"should": []any{
							map[string]any{"match": map[string]any{"name": map[string]any{"query": q, "fuzziness": "AUTO"}}},
							map[string]any{"term": map[string]any{"sku": q}},
							map[string]any{"term": map[string]any{"barcode": q}},
							map[string]any{"term": map[string]any{"slot": q}},
						},
						"minimum_should_match": 1,
					}},
				},
			},
		},
	}
}

// SearchProducts runs the fuzzy multi-field search within one company.
func (e *ES) SearchProducts(ctx context.Context, companyID uuid.UUID, q string, limit int) ([]domain.SearchHit, int, error) {
	body, err := json.Marshal(BuildSearchQuery(companyID, q, limit))
	if err != nil {
		return nil, 0, fmt.Errorf("marshal search query: %w", err)
	}
	res, err := e.client.Search(
		e.client.Search.WithContext(ctx),
		e.client.Search.WithIndex(IndexName),
		e.client.Search.WithBody(bytes.NewReader(body)))
	if err != nil {
		return nil, 0, fmt.Errorf("search request: %w", err)
	}
	defer res.Body.Close()
	if res.IsError() {
		raw, _ := io.ReadAll(res.Body)
		return nil, 0, fmt.Errorf("search request: %s", raw)
	}

	var parsed struct {
		Hits struct {
			Total struct {
				Value int `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID     string `json:"_id"`
				Source struct {
					Name        string   `json:"name"`
					SKU         string   `json:"sku"`
					Barcode     []string `json:"barcode"`
					RetailPrice float64  `json:"retail_price"`
					Slot        []string `json:"slot"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, 0, fmt.Errorf("decode search response: %w", err)
	}

	hits := make([]domain.SearchHit, 0, len(parsed.Hits.Hits))
	for _, h := range parsed.Hits.Hits {
		if h.Source.Barcode == nil {
			h.Source.Barcode = []string{}
		}
		if h.Source.Slot == nil {
			h.Source.Slot = []string{}
		}
		hits = append(hits, domain.SearchHit{
			ID:          h.ID,
			Name:        h.Source.Name,
			SKU:         h.Source.SKU,
			Barcode:     h.Source.Barcode,
			RetailPrice: h.Source.RetailPrice,
			Slot:        h.Source.Slot,
		})
	}
	return hits, parsed.Hits.Total.Value, nil
}
