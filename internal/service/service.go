package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"pricetags/internal/domain"
	"pricetags/internal/search"
	"pricetags/internal/storage"
)

const esSyncTimeout = 10 * time.Second

// Service orchestrates Postgres (source of truth) and Elasticsearch
// (search projection). ES sync runs in a background goroutine after the
// DB commit: the client response never waits for the projection and an
// ES failure never rolls back committed data.
type Service struct {
	repo *storage.Repo
	es   *search.ES
	log  *slog.Logger
	wg   sync.WaitGroup
}

func New(repo *storage.Repo, es *search.ES, log *slog.Logger) *Service {
	return &Service{repo: repo, es: es, log: log}
}

// syncES runs an Elasticsearch projection update in the background.
// A fresh context is used on purpose: the sync must survive the request
// context being canceled once the response is written.
func (s *Service) syncES(op string, fn func(ctx context.Context) error) {
	s.wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), esSyncTimeout)
		defer cancel()
		if err := fn(ctx); err != nil {
			s.log.Error("es sync failed", "op", op, "err", err)
		}
	})
}

// Wait blocks until in-flight projection syncs finish; called on
// graceful shutdown so no ES update is lost on SIGTERM.
func (s *Service) Wait() {
	s.wg.Wait()
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

	s.syncES("product upsert", func(ctx context.Context) error {
		return s.es.UpsertProducts(ctx, docs)
	})
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
		s.syncES("slot update", func(ctx context.Context) error {
			// Read the state after commit so concurrent assignments
			// converge on the latest placement.
			current, err := s.repo.ProductSlots(ctx, companyID, affected)
			if err != nil {
				return err
			}
			updates := make(map[uuid.UUID][]string, len(affected))
			for _, id := range affected {
				updates[id] = current[id] // nil => product left without slots
			}
			return s.es.UpdateSlots(ctx, updates)
		})
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
	s.syncES("product delete", func(ctx context.Context) error {
		return s.es.DeleteProducts(ctx, deleted)
	})
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
