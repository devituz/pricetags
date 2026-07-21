package httpapi

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"pricetags/internal/domain"
	"pricetags/internal/service"
)

const (
	maxBulkProducts = 1000
	maxBulkSlots    = 1000
	maxBulkDeletes  = 1000
	maxSlotNumber   = 1_000_000_000 // fits int4, sane board size cap
	defaultLimit    = 20
	maxLimit        = 100
)

type handler struct {
	svc *service.Service
}

type bulkProductsRequest struct {
	Products []domain.BulkProductInput `json:"products"`
}

func (h *handler) bulkUpsertProducts(c *fiber.Ctx) error {
	var req bulkProductsRequest
	if err := c.BodyParser(&req); err != nil {
		return domain.Validationf("invalid json body: %v", err)
	}
	if len(req.Products) == 0 {
		return domain.Validationf("products list is empty")
	}
	if len(req.Products) > maxBulkProducts {
		return domain.Validationf("products list exceeds %d items", maxBulkProducts)
	}
	for i := range req.Products {
		p := &req.Products[i]
		p.Name = strings.TrimSpace(p.Name)
		p.SKU = strings.TrimSpace(p.SKU)
		switch {
		case p.Name == "":
			return domain.Validationf("products[%d]: name is required", i)
		case p.SKU == "":
			return domain.Validationf("products[%d]: sku is required", i)
		case p.SupplyPrice.IsNegative(), p.RetailPrice.IsNegative():
			return domain.Validationf("products[%d]: prices must be non-negative", i)
		}
	}

	res, err := h.svc.BulkUpsertProducts(c.UserContext(), companyID(c), req.Products)
	if err != nil {
		return err
	}
	return c.JSON(res)
}

func (h *handler) assignSlots(c *fiber.Ctx) error {
	var items []domain.SlotAssignment
	if err := c.BodyParser(&items); err != nil {
		return domain.Validationf("invalid json body: %v", err)
	}
	if len(items) == 0 {
		return domain.Validationf("slots list is empty")
	}
	if len(items) > maxBulkSlots {
		return domain.Validationf("slots list exceeds %d items", maxBulkSlots)
	}
	for i, s := range items {
		if s.Slot < 1 || s.Slot > maxSlotNumber {
			return domain.Validationf("slots[%d]: slot must be between 1 and %d", i, maxSlotNumber)
		}
	}

	updated, err := h.svc.AssignSlots(c.UserContext(), companyID(c), items)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"updated": updated})
}

func (h *handler) listSlots(c *fiber.Ctx) error {
	page := max(c.QueryInt("page", 1), 1)
	limit := c.QueryInt("limit", defaultLimit)
	if limit < 1 || limit > maxLimit {
		limit = defaultLimit
	}
	search := c.Query("search")

	items, total, err := h.svc.ListSlots(c.UserContext(), companyID(c), search, limit, (page-1)*limit)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

type deleteProductsRequest struct {
	IDs []uuid.UUID `json:"ids"`
}

func (h *handler) deleteProducts(c *fiber.Ctx) error {
	var req deleteProductsRequest
	if err := c.BodyParser(&req); err != nil {
		return domain.Validationf("invalid json body: %v", err)
	}
	if len(req.IDs) == 0 {
		return domain.Validationf("ids list is empty")
	}
	if len(req.IDs) > maxBulkDeletes {
		return domain.Validationf("ids list exceeds %d items", maxBulkDeletes)
	}

	deleted, err := h.svc.DeleteProducts(c.UserContext(), companyID(c), req.IDs)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"deleted": deleted})
}

func (h *handler) stockValue(c *fiber.Ctx) error {
	report, err := h.svc.StockValue(c.UserContext(), companyID(c))
	if err != nil {
		return err
	}
	return c.JSON(report)
}

func (h *handler) searchProducts(c *fiber.Ctx) error {
	q := c.Query("q")
	if q == "" {
		return domain.Validationf("query parameter q is required")
	}
	limit := c.QueryInt("limit", defaultLimit)
	if limit < 1 || limit > maxLimit {
		limit = defaultLimit
	}

	hits, total, err := h.svc.SearchProducts(c.UserContext(), companyID(c), q, limit)
	if err != nil {
		return err
	}
	return c.JSON(fiber.Map{"items": hits, "total": total})
}
