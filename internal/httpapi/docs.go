package httpapi

import (
	_ "embed"

	"github.com/gofiber/fiber/v2"
)

//go:embed openapi.yaml
var openapiSpec []byte

// Swagger UI is loaded from CDN: the spec itself is embedded in the
// binary, so /docs works anywhere the container can reach the internet.
const swaggerHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Pricetags API — Swagger UI</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  SwaggerUIBundle({
    url: "/openapi.yaml",
    dom_id: "#swagger-ui",
    persistAuthorization: true,
  });
</script>
</body>
</html>`

func registerDocs(app *fiber.App) {
	app.Get("/openapi.yaml", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, "application/yaml")
		return c.Send(openapiSpec)
	})
	app.Get("/docs", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.SendString(swaggerHTML)
	})
}
