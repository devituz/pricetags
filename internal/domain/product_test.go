package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestDedupeBySKU(t *testing.T) {
	p := func(sku string, price int64) BulkProductInput {
		return BulkProductInput{Name: "n-" + sku, SKU: sku, RetailPrice: decimal.NewFromInt(price)}
	}

	tests := []struct {
		name string
		in   []BulkProductInput
		want []BulkProductInput
	}{
		{
			name: "empty",
			in:   nil,
			want: []BulkProductInput{},
		},
		{
			name: "no duplicates",
			in:   []BulkProductInput{p("a", 1), p("b", 2)},
			want: []BulkProductInput{p("a", 1), p("b", 2)},
		},
		{
			name: "last occurrence wins, first position kept",
			in:   []BulkProductInput{p("a", 1), p("b", 2), p("a", 3)},
			want: []BulkProductInput{p("a", 3), p("b", 2)},
		},
		{
			name: "triple duplicate",
			in:   []BulkProductInput{p("a", 1), p("a", 2), p("a", 3)},
			want: []BulkProductInput{p("a", 3)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DedupeBySKU(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i].SKU != tt.want[i].SKU || !got[i].RetailPrice.Equal(tt.want[i].RetailPrice) {
					t.Errorf("item %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDedupeSlots(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()

	in := []SlotAssignment{
		{Slot: 5, ProductID: &id1},
		{Slot: 7, ProductID: &id2},
		{Slot: 5, ProductID: &id2}, // overrides the first assignment
		{Slot: 9, ProductID: nil},  // explicit clear
	}
	got := DedupeSlots(in)

	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	if got[0].Slot != 5 || got[0].ProductID == nil || *got[0].ProductID != id2 {
		t.Errorf("slot 5 = %+v, want product %s", got[0], id2)
	}
	if got[2].Slot != 9 || got[2].ProductID != nil {
		t.Errorf("slot 9 = %+v, want cleared", got[2])
	}
}
