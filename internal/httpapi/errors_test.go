package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"pricetags/internal/domain"
)

// The API error contract: every failure is a JSON body with a proper
// HTTP status, tenant header is enforced before any business logic,
// and internal error details never leak to the client.
func testApp(routeErr error) *fiber.App {
	app := fiber.New(fiber.Config{
		ErrorHandler: errorHandler(slog.New(slog.DiscardHandler)),
	})
	app.Get("/probe", requireCompany, func(c *fiber.Ctx) error {
		return routeErr
	})
	return app
}

func TestErrorContract(t *testing.T) {
	tests := []struct {
		name       string
		companyHdr string
		routeErr   error
		wantCode   int
		wantErrMsg string
	}{
		{
			name:       "missing company header",
			companyHdr: "",
			wantCode:   fiber.StatusBadRequest,
			wantErrMsg: "X-Company-Id header is required",
		},
		{
			name:       "malformed company header",
			companyHdr: "not-a-uuid",
			wantCode:   fiber.StatusBadRequest,
			wantErrMsg: "X-Company-Id must be a valid uuid",
		},
		{
			name:       "validation error maps to 400",
			companyHdr: "11111111-1111-1111-1111-111111111111",
			routeErr:   domain.Validationf("products[3]: sku is required"),
			wantCode:   fiber.StatusBadRequest,
			wantErrMsg: "products[3]: sku is required",
		},
		{
			name:       "internal error is a generic 500, details stay server-side",
			companyHdr: "11111111-1111-1111-1111-111111111111",
			routeErr:   errors.New("pq: connection refused on 10.0.0.5"),
			wantCode:   fiber.StatusInternalServerError,
			wantErrMsg: "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := testApp(tt.routeErr)
			req := httptest.NewRequest("GET", "/probe", nil)
			if tt.companyHdr != "" {
				req.Header.Set("X-Company-Id", tt.companyHdr)
			}

			resp, err := app.Test(req)
			if err != nil {
				t.Fatalf("request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantCode {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantCode)
			}
			if ct := resp.Header.Get("Content-Type"); ct != fiber.MIMEApplicationJSON {
				t.Errorf("content-type = %q, want %q", ct, fiber.MIMEApplicationJSON)
			}

			raw, _ := io.ReadAll(resp.Body)
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("body is not JSON: %s", raw)
			}
			if body.Error != tt.wantErrMsg {
				t.Errorf("error = %q, want %q", body.Error, tt.wantErrMsg)
			}
		})
	}
}
