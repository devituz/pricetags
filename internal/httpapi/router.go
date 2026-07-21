package httpapi

import (
	"errors"
	"log/slog"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/google/uuid"

	"pricetags/internal/domain"
	"pricetags/internal/service"
)

const companyIDKey = "company_id"

func New(svc *service.Service, log *slog.Logger) *fiber.App {
	app := fiber.New(fiber.Config{
		AppName:               "pricetags",
		DisableStartupMessage: true,
		ErrorHandler:          errorHandler(log),
	})
	app.Use(recover.New())

	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"status": "ok"})
	})

	h := &handler{svc: svc}
	api := app.Group("", requireCompany)
	api.Post("/products/bulk", h.bulkUpsertProducts)
	api.Delete("/products", h.deleteProducts)
	api.Get("/products/search", h.searchProducts)
	api.Put("/slots", h.assignSlots)
	api.Get("/slots", h.listSlots)
	api.Get("/reports/stock-value", h.stockValue)

	return app
}

// requireCompany enforces tenant scoping: every business route needs a
// valid X-Company-Id, and every query below filters by it.
func requireCompany(c *fiber.Ctx) error {
	raw := c.Get("X-Company-Id")
	if raw == "" {
		return domain.Validationf("X-Company-Id header is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return domain.Validationf("X-Company-Id must be a valid uuid")
	}
	c.Locals(companyIDKey, id)
	return c.Next()
}

func companyID(c *fiber.Ctx) uuid.UUID {
	return c.Locals(companyIDKey).(uuid.UUID)
}

func errorHandler(log *slog.Logger) fiber.ErrorHandler {
	return func(c *fiber.Ctx, err error) error {
		var vErr domain.ValidationError
		if errors.As(err, &vErr) {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": vErr.Msg})
		}
		var fErr *fiber.Error
		if errors.As(err, &fErr) {
			return c.Status(fErr.Code).JSON(fiber.Map{"error": fErr.Message})
		}
		log.Error("request failed",
			"method", c.Method(), "path", c.Path(), "err", err)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "internal server error"})
	}
}
