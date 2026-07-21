package domain

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// BulkProductInput is one item of the POST /products/bulk payload.
// JSON tags double as jsonb_to_recordset column names in the repository.
type BulkProductInput struct {
	Name        string          `json:"name"`
	SKU         string          `json:"sku"`
	Barcode     []string        `json:"barcode"`
	SupplyPrice decimal.Decimal `json:"supply_price"`
	RetailPrice decimal.Decimal `json:"retail_price"`
}

// DedupeBySKU keeps the last occurrence of each sku, preserving the
// position of the first occurrence. Required before the multi-row
// ON CONFLICT upsert: Postgres rejects a statement that updates the
// same row twice ("cannot affect row a second time").
func DedupeBySKU(in []BulkProductInput) []BulkProductInput {
	out := make([]BulkProductInput, 0, len(in))
	pos := make(map[string]int, len(in))
	for _, p := range in {
		if i, ok := pos[p.SKU]; ok {
			out[i] = p
			continue
		}
		pos[p.SKU] = len(out)
		out = append(out, p)
	}
	return out
}

type BulkUpsertResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
	Total   int `json:"total"`
}

// SlotAssignment is one item of the PUT /slots payload.
// ProductID == nil clears (or pre-creates) the slot.
type SlotAssignment struct {
	Slot      int        `json:"slot"`
	ProductID *uuid.UUID `json:"product_id"`
}

// DedupeSlots keeps the last assignment for each slot number.
func DedupeSlots(in []SlotAssignment) []SlotAssignment {
	out := make([]SlotAssignment, 0, len(in))
	pos := make(map[int]int, len(in))
	for _, s := range in {
		if i, ok := pos[s.Slot]; ok {
			out[i] = s
			continue
		}
		pos[s.Slot] = len(out)
		out = append(out, s)
	}
	return out
}

// SlotView is one row of the GET /slots board.
type SlotView struct {
	Slot    int          `json:"slot"`
	Product *SlotProduct `json:"product"` // nil => empty slot
}

type SlotProduct struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	SKU         string          `json:"sku"`
	RetailPrice decimal.Decimal `json:"retail_price"`
}

type StockValueReport struct {
	TotalSupplyValue decimal.Decimal `json:"total_supply_value"`
	OccupiedSlots    int             `json:"occupied_slots"`
	EmptySlots       int             `json:"empty_slots"`
}

// SearchHit is one Elasticsearch result of GET /products/search.
type SearchHit struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	SKU         string   `json:"sku"`
	Barcode     []string `json:"barcode"`
	RetailPrice float64  `json:"retail_price"`
	Slot        []string `json:"slot"`
}
