package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/shopspring/decimal"

	"pricetags/internal/domain"
)

type Repo struct {
	db *sqlx.DB
}

func NewRepo(db *sqlx.DB) *Repo {
	return &Repo{db: db}
}

type UpsertedProduct struct {
	ID       uuid.UUID `db:"id"`
	SKU      string    `db:"sku"`
	Inserted bool      `db:"inserted"`
}

// BulkUpsertProducts upserts a deduplicated batch on (company_id, sku).
// Input must already be deduplicated: a multi-row ON CONFLICT statement
// cannot update the same row twice.
func (r *Repo) BulkUpsertProducts(ctx context.Context, companyID uuid.UUID, items []domain.BulkProductInput) ([]UpsertedProduct, error) {
	payload, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("marshal bulk payload: %w", err)
	}
	const q = `
		INSERT INTO product (company_id, name, sku, barcode, supply_price, retail_price)
		SELECT $1, x.name, x.sku, COALESCE(x.barcode, '{}'), x.supply_price, x.retail_price
		FROM jsonb_to_recordset($2::jsonb)
			AS x(name text, sku text, barcode text[], supply_price numeric, retail_price numeric)
		ON CONFLICT (company_id, sku) WHERE deleted_at IS NULL
		DO UPDATE SET
			name         = EXCLUDED.name,
			barcode      = EXCLUDED.barcode,
			supply_price = EXCLUDED.supply_price,
			retail_price = EXCLUDED.retail_price,
			updated_at   = now()
		RETURNING id, sku, (xmax = 0) AS inserted`
	var out []UpsertedProduct
	if err := sqlx.SelectContext(ctx, r.db, &out, q, companyID, payload); err != nil {
		return nil, fmt.Errorf("bulk upsert products: %w", err)
	}
	return out, nil
}

// AssignSlots applies slot assignments atomically and returns product ids
// whose slot placement changed (new occupants + displaced ones) so the
// caller can sync Elasticsearch.
func (r *Repo) AssignSlots(ctx context.Context, companyID uuid.UUID, items []domain.SlotAssignment) ([]uuid.UUID, error) {
	slotNumbers := make([]int64, 0, len(items))
	productIDs := make([]string, 0, len(items))
	for _, it := range items {
		slotNumbers = append(slotNumbers, int64(it.Slot))
		if it.ProductID != nil {
			productIDs = append(productIDs, it.ProductID.String())
		}
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Reject product ids that do not exist, are soft-deleted, or belong
	// to another company (tenant isolation).
	if len(productIDs) > 0 {
		var known []string
		const checkQ = `SELECT id::text FROM product
			WHERE company_id = $1 AND id = ANY($2::uuid[]) AND deleted_at IS NULL`
		if err := sqlx.SelectContext(ctx, tx, &known, checkQ, companyID, pq.Array(productIDs)); err != nil {
			return nil, fmt.Errorf("check products: %w", err)
		}
		knownSet := make(map[string]struct{}, len(known))
		for _, id := range known {
			knownSet[id] = struct{}{}
		}
		for _, id := range productIDs {
			if _, ok := knownSet[id]; !ok {
				return nil, domain.Validationf("unknown product_id %s", id)
			}
		}
	}

	// Lock touched slots (ordered => no deadlock between concurrent
	// requests) and remember current occupants: they get displaced.
	var displaced []string
	const lockQ = `SELECT DISTINCT product_id::text FROM (
			SELECT product_id FROM shelf_slot
			WHERE company_id = $1 AND slot_number = ANY($2::int[])
			ORDER BY slot_number
			FOR UPDATE
		) t WHERE product_id IS NOT NULL`
	if err := sqlx.SelectContext(ctx, tx, &displaced, lockQ, companyID, pq.Array(slotNumbers)); err != nil {
		return nil, fmt.Errorf("lock slots: %w", err)
	}

	payload, err := json.Marshal(items)
	if err != nil {
		return nil, fmt.Errorf("marshal slot payload: %w", err)
	}
	const upsertQ = `
		INSERT INTO shelf_slot (company_id, slot_number, product_id)
		SELECT $1, x.slot, x.product_id
		FROM jsonb_to_recordset($2::jsonb) AS x(slot int, product_id uuid)
		ORDER BY x.slot
		ON CONFLICT (company_id, slot_number)
		DO UPDATE SET product_id = EXCLUDED.product_id, updated_at = now()`
	if _, err := tx.ExecContext(ctx, upsertQ, companyID, payload); err != nil {
		return nil, fmt.Errorf("upsert slots: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	affected := make(map[uuid.UUID]struct{}, len(productIDs)+len(displaced))
	for _, it := range items {
		if it.ProductID != nil {
			affected[*it.ProductID] = struct{}{}
		}
	}
	for _, id := range displaced {
		affected[uuid.MustParse(id)] = struct{}{}
	}
	out := make([]uuid.UUID, 0, len(affected))
	for id := range affected {
		out = append(out, id)
	}
	return out, nil
}

// ProductSlots returns current slot numbers (as strings, ordered) for the
// given products. Products without a slot are absent from the map.
func (r *Repo) ProductSlots(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID][]string, error) {
	if len(ids) == 0 {
		return map[uuid.UUID][]string{}, nil
	}
	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = id.String()
	}
	rows := []struct {
		ProductID uuid.UUID      `db:"product_id"`
		Slots     pq.StringArray `db:"slots"`
	}{}
	const q = `SELECT product_id, array_agg(slot_number::text ORDER BY slot_number) AS slots
		FROM shelf_slot
		WHERE company_id = $1 AND product_id = ANY($2::uuid[])
		GROUP BY product_id`
	if err := sqlx.SelectContext(ctx, r.db, &rows, q, companyID, pq.Array(strIDs)); err != nil {
		return nil, fmt.Errorf("product slots: %w", err)
	}
	out := make(map[uuid.UUID][]string, len(rows))
	for _, row := range rows {
		out[row.ProductID] = row.Slots
	}
	return out, nil
}

type slotRow struct {
	SlotNumber  int              `db:"slot_number"`
	ProductID   *uuid.UUID       `db:"product_id"`
	Name        sql.NullString   `db:"name"`
	SKU         sql.NullString   `db:"sku"`
	RetailPrice *decimal.Decimal `db:"retail_price"`
}

// searchUnionQ joins products found via trigram indexes back to slots and
// unions the slot-number branch separately: a single OR across both tables
// forces a full walk of the company board (see EXPLAIN in README), while
// the UNION lets each branch use its own index.
const searchUnionQ = `
	SELECT s.slot_number, s.product_id, p.name, p.sku, p.retail_price
	FROM shelf_slot s
	JOIN product p ON p.id = s.product_id AND p.deleted_at IS NULL
	WHERE s.company_id = $1 AND (p.name ILIKE $2 OR p.sku ILIKE $2)
	UNION
	SELECT s.slot_number, s.product_id, p.name, p.sku, p.retail_price
	FROM shelf_slot s
	LEFT JOIN product p ON p.id = s.product_id AND p.deleted_at IS NULL
	WHERE s.company_id = $1 AND s.slot_number = $3`

// ListSlots returns one page of the company board (empty slots included)
// plus the total count for the same filter.
func (r *Repo) ListSlots(ctx context.Context, companyID uuid.UUID, search string, limit, offset int) ([]domain.SlotView, int, error) {
	if search == "" {
		return r.listAllSlots(ctx, companyID, limit, offset)
	}

	pattern := "%" + escapeLike(search) + "%"
	// Slot numbers start at 1, so 0 means "no slot-number match".
	slotNum := 0
	if n, err := strconv.Atoi(search); err == nil {
		slotNum = n
	}

	var rows []slotRow
	listQ := `SELECT * FROM (` + searchUnionQ + `) t ORDER BY slot_number LIMIT $4 OFFSET $5`
	if err := sqlx.SelectContext(ctx, r.db, &rows, listQ, companyID, pattern, slotNum, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("search slots: %w", err)
	}

	var total int
	countQ := `SELECT count(*) FROM (` + searchUnionQ + `) t`
	if err := sqlx.GetContext(ctx, r.db, &total, countQ, companyID, pattern, slotNum); err != nil {
		return nil, 0, fmt.Errorf("count searched slots: %w", err)
	}

	return slotViews(rows), total, nil
}

func (r *Repo) listAllSlots(ctx context.Context, companyID uuid.UUID, limit, offset int) ([]domain.SlotView, int, error) {
	const listQ = `
		SELECT s.slot_number, s.product_id, p.name, p.sku, p.retail_price
		FROM shelf_slot s
		LEFT JOIN product p ON p.id = s.product_id AND p.deleted_at IS NULL
		WHERE s.company_id = $1
		ORDER BY s.slot_number
		LIMIT $2 OFFSET $3`
	var rows []slotRow
	if err := sqlx.SelectContext(ctx, r.db, &rows, listQ, companyID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("list slots: %w", err)
	}

	var total int
	const countQ = `SELECT count(*) FROM shelf_slot WHERE company_id = $1`
	if err := sqlx.GetContext(ctx, r.db, &total, countQ, companyID); err != nil {
		return nil, 0, fmt.Errorf("count slots: %w", err)
	}

	return slotViews(rows), total, nil
}

// escapeLike neutralizes user-supplied LIKE wildcards.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func slotViews(rows []slotRow) []domain.SlotView {
	out := make([]domain.SlotView, 0, len(rows))
	for _, row := range rows {
		view := domain.SlotView{Slot: row.SlotNumber}
		if row.ProductID != nil && row.Name.Valid {
			view.Product = &domain.SlotProduct{
				ID:          *row.ProductID,
				Name:        row.Name.String,
				SKU:         row.SKU.String,
				RetailPrice: *row.RetailPrice,
			}
		}
		out = append(out, view)
	}
	return out
}

// SoftDeleteProducts marks products deleted and frees their slots.
// Returns ids that were actually deleted (existing, not yet deleted,
// owned by the company).
func (r *Repo) SoftDeleteProducts(ctx context.Context, companyID uuid.UUID, ids []uuid.UUID) ([]uuid.UUID, error) {
	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = id.String()
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var deleted []uuid.UUID
	const delQ = `UPDATE product
		SET deleted_at = now(), updated_at = now()
		WHERE company_id = $1 AND id = ANY($2::uuid[]) AND deleted_at IS NULL
		RETURNING id`
	if err := sqlx.SelectContext(ctx, tx, &deleted, delQ, companyID, pq.Array(strIDs)); err != nil {
		return nil, fmt.Errorf("soft delete products: %w", err)
	}

	if len(deleted) > 0 {
		delStr := make([]string, len(deleted))
		for i, id := range deleted {
			delStr[i] = id.String()
		}
		const freeQ = `UPDATE shelf_slot
			SET product_id = NULL, updated_at = now()
			WHERE company_id = $1 AND product_id = ANY($2::uuid[])`
		if _, err := tx.ExecContext(ctx, freeQ, companyID, pq.Array(delStr)); err != nil {
			return nil, fmt.Errorf("free slots: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}
	return deleted, nil
}

// StockValue computes the whole report in a single SQL statement.
func (r *Repo) StockValue(ctx context.Context, companyID uuid.UUID) (domain.StockValueReport, error) {
	row := struct {
		Total    decimal.Decimal `db:"total_supply_value"`
		Occupied int             `db:"occupied_slots"`
		Empty    int             `db:"empty_slots"`
	}{}
	// A product placed on several slots is counted once in the value sum
	// ("sum of attached products"), while slot counters stay per-slot.
	const q = `
		WITH board AS (
			SELECT s.slot_number, p.id AS product_id, p.supply_price
			FROM shelf_slot s
			LEFT JOIN product p ON p.id = s.product_id AND p.deleted_at IS NULL
			WHERE s.company_id = $1
		)
		SELECT
			COALESCE((SELECT SUM(supply_price) FROM (
				SELECT DISTINCT product_id, supply_price FROM board WHERE product_id IS NOT NULL
			) d), 0)                                         AS total_supply_value,
			COUNT(*) FILTER (WHERE product_id IS NOT NULL)   AS occupied_slots,
			COUNT(*) FILTER (WHERE product_id IS NULL)       AS empty_slots
		FROM board`
	if err := sqlx.GetContext(ctx, r.db, &row, q, companyID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.StockValueReport{TotalSupplyValue: decimal.Zero}, nil
		}
		return domain.StockValueReport{}, fmt.Errorf("stock value report: %w", err)
	}
	return domain.StockValueReport{
		TotalSupplyValue: row.Total,
		OccupiedSlots:    row.Occupied,
		EmptySlots:       row.Empty,
	}, nil
}
