package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"pricetags/internal/domain"
	"pricetags/internal/search"
	"pricetags/internal/storage"
)

// Service orchestrates Postgres (source of truth) and Elasticsearch
// (search projection). ES sync runs after the DB commit: an ES failure
// never rolls back committed data, it is logged and surfaced as an error
// of the projection, not of the write.
type Service struct {
	repo *storage.Repo
	es   *search.ES
	log  *slog.Logger
}

func New(repo *storage.Repo, es *search.ES, log *slog.Logger) *Service {
	return &Service{repo: repo, es: es, log: log}
}

// BulkUpsertProducts deduplicates the batch (last sku wins), upserts into
// Postgres and merges the product fields into ES documents.
func (s *Service) BulkUpsertProducts(ctx context.Context, companyID uuid.UUID, items []domain.BulkProductInput) (domain.BulkUpsertResult, error) {
	deduped := domain.DedupeBySKU(items)

	rows, err := s.repo.BulkUpsertProducts(ctx, companyID, deduped)
	if err != nil {
		return domain.BulkUpsertResult{}, err
	}

	bySKU := make(map[string]domain.BulkProductInput, len(deduped))
	for _, it := range deduped {
		bySKU[it.SKU] = it
	}
	docs := make(map[uuid.UUID]search.ProductDoc, len(rows))
	res := domain.BulkUpsertResult{Total: len(rows)}
	for _, row := range rows {
		if row.Inserted {
			res.Created++
		} else {
			res.Updated++
		}
		it := bySKU[row.SKU]
		barcode := it.Barcode
		if barcode == nil {
			barcode = []string{}
		}
		docs[row.ID] = search.ProductDoc{
			ID:          row.ID.String(),
			CompanyID:   companyID.String(),
			Name:        it.Name,
			SKU:         it.SKU,
			Barcode:     barcode,
			RetailPrice: it.RetailPrice.InexactFloat64(),
		}
	}

	if err := s.es.UpsertProducts(ctx, docs); err != nil {
		s.log.Error("es sync failed after product upsert", "err", err)
	}
	return res, nil
}

// AssignSlots applies assignments and refreshes the slot field of every
// affected ES document (new occupants and displaced products).
func (s *Service) AssignSlots(ctx context.Context, companyID uuid.UUID, items []domain.SlotAssignment) (int, error) {
	deduped := domain.DedupeSlots(items)

	affected, err := s.repo.AssignSlots(ctx, companyID, deduped)
	if err != nil {
		return 0, err
	}

	if len(affected) > 0 {
		current, err := s.repo.ProductSlots(ctx, companyID, affected)
		if err != nil {
			return 0, err
		}
		updates := make(map[uuid.UUID][]string, len(affected))
		for _, id := range affected {
			updates[id] = current[id] // nil => product left without slots
		}
		if err := s.es.UpdateSlots(ctx, updates); err != nil {
			s.log.Error("es sync failed after slot assignment", "err", err)
		}
	}
	return len(deduped), nil
}

func (s *Service) ListSlots(ctx context.Context, companyID uuid.UUID, searchTerm string, limit, offset int) ([]domain.SlotView, int, error) {
	return s.repo.ListSlots(ctx, companyID, searchTerm, limit, offset)
}

// DeleteProducts soft-deletes products, frees their slots and removes
// their ES documents.
func (s *Service) DeleteProducts(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (int, error) {
	deleted, err := s.repo.SoftDeleteProducts(ctx, companyID, ids)
	if err != nil {
		return 0, err
	}
	if err := s.es.DeleteProducts(ctx, deleted); err != nil {
		s.log.Error("es sync failed after product delete", "err", err)
	}
	return len(deleted), nil
}

func (s *Service) StockValue(ctx context.Context, companyID uuid.UUID) (domain.StockValueReport, error) {
	return s.repo.StockValue(ctx, companyID)
}

func (s *Service) SearchProducts(ctx context.Context, companyID uuid.UUID, q string, limit int) ([]domain.SearchHit, int, error) {
	hits, total, err := s.es.SearchProducts(ctx, companyID, q, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("product search: %w", err)
	}
	return hits, total, nil
}
