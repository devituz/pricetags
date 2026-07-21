package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/shopspring/decimal"

	"pricetags/internal/config"
	"pricetags/internal/httpapi"
	"pricetags/internal/search"
	"pricetags/internal/service"
	"pricetags/internal/storage"
)

func main() {
	// Prices are serialized as JSON numbers, not strings.
	decimal.MarshalJSONWithoutQuotes = true

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()

	db, err := storage.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Error("postgres connect failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if cfg.MigrateOnStart {
		if err := storage.Migrate(db); err != nil {
			log.Error("migrations failed", "err", err)
			os.Exit(1)
		}
		log.Info("migrations applied")
	}

	es, err := search.New(cfg.ElasticURL, log)
	if err != nil {
		log.Error("elasticsearch client failed", "err", err)
		os.Exit(1)
	}
	if err := es.EnsureIndex(context.Background()); err != nil {
		log.Error("elasticsearch index setup failed", "err", err)
		os.Exit(1)
	}

	svc := service.New(storage.NewRepo(db), es, log)
	app := httpapi.New(svc, log)

	errCh := make(chan error, 1)
	go func() {
		log.Info("http server starting", "addr", cfg.HTTPAddr)
		errCh <- app.Listen(cfg.HTTPAddr)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-stop:
		log.Info("shutting down", "signal", sig.String())
		if err := app.ShutdownWithTimeout(cfg.ShutdownTimeout); err != nil {
			log.Error("graceful shutdown failed", "err", err)
			os.Exit(1)
		}
		log.Info("server stopped")
	case err := <-errCh:
		log.Error("http server failed", "err", err)
		os.Exit(1)
	}
}
