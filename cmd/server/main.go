package main

import (
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"shared-clipboard/internal/app"
)

func main() {
	cfg := app.Config{
		DataDir:                requireEnv("APP_DATA_DIR"),
		BootstrapAdminUsername: requireEnv("APP_BOOTSTRAP_ADMIN_USERNAME"),
		BootstrapAdminPassword: requireEnv("APP_BOOTSTRAP_ADMIN_PASSWORD"),
		SessionSecret:          requireEnv("APP_SESSION_SECRET"),
		CookieSecure:           getenv("APP_COOKIE_SECURE", "true") != "false",
		StaticDir:              getenv("APP_STATIC_DIR", filepath.Join(".", "web", "dist")),
		PublicBaseURL:          strings.TrimRight(getenv("APP_PUBLIC_BASE_URL", ""), "/"),
		AllowedOrigins:         splitCSV(getenv("APP_ALLOWED_ORIGINS", "")),
		AllowInsecureHTTP:      getenv("APP_ALLOW_INSECURE_HTTP", "false") == "true",
	}
	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("init app: %v", err)
	}
	addr := getenv("APP_ADDR", ":2053")
	server := &http.Server{
		Addr:    addr,
		Handler: application.Handler(),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	cert := os.Getenv("APP_TLS_CERT")
	key := os.Getenv("APP_TLS_KEY")
	log.Printf("shared-clipboard listening on %s", addr)
	if cert == "" || key == "" {
		if !cfg.AllowInsecureHTTP {
			log.Fatal("APP_TLS_CERT and APP_TLS_KEY are required unless APP_ALLOW_INSECURE_HTTP=true")
		}
		log.Fatal(server.ListenAndServe())
	}
	log.Fatal(server.ListenAndServeTLS(cert, key))
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func requireEnv(key string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		log.Fatalf("missing required environment variable: %s", key)
	}
	return value
}

func splitCSV(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
