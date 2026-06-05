package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL  string
	JWTSecret    string
	GoAPIPort    string
	PythonAIURL  string
	AppEnv       string // "development" | "production"
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, reading from environment")
	}

	return &Config{
		DatabaseURL: required("DATABASE_URL"),
		JWTSecret:   required("JWT_SECRET"),
		GoAPIPort:   getOr("GO_API_PORT", "8000"),
		PythonAIURL: getOr("PYTHON_AI_URL", "http://localhost:8001"),
		AppEnv:      getOr("APP_ENV", "development"),
	}
}

func (c *Config) IsProduction() bool {
	return c.AppEnv == "production"
}

func required(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s is not set", key)
	}
	return v
}

func getOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
