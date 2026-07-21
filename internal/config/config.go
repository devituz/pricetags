package config

import (
	"os"
	"time"
)

type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	ElasticURL      string
	MigrateOnStart  bool
	ShutdownTimeout time.Duration
}

func Load() Config {
	return Config{
		HTTPAddr:        getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:     getenv("DATABASE_URL", "postgres://pricetags:pricetags@localhost:5433/pricetags?sslmode=disable"),
		ElasticURL:      getenv("ELASTICSEARCH_URL", "http://localhost:9200"),
		MigrateOnStart:  getenv("MIGRATE_ON_START", "true") == "true",
		ShutdownTimeout: 10 * time.Second,
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
