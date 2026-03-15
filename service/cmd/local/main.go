// cmd/local/main.go — Local dev server (plain net/http, no Lambda).
//
// Usage:
//
//	go run ./cmd/local
//	PORT=8090 BUCKET_NAME=521studios-staging-pfsrd2-data go run ./cmd/local
//
// With Air hot-reload this is recompiled automatically on source changes.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"

	"github.com/521studios/pfsrd2-data-api/internal/api"
	apps3 "github.com/521studios/pfsrd2-data-api/internal/s3"
	"github.com/521studios/pfsrd2-data-api/internal/startup"
)

func main() {
	ctx := context.Background()

	bucket := envOrDefault("BUCKET_NAME", "521studios-staging-pfsrd2-data")
	imageDomain := envOrDefault("IMAGE_DOMAIN", "images.pfsrd2.521studios.com")
	port := envOrDefault("PORT", "8090")

	awsCfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}

	s3Client := apps3.New(awsCfg, bucket)

	startupCfg := startup.Config{
		BucketName:    bucket,
		ImageDomain:   imageDomain,
		WatchInterval: os.Getenv("WATCHER_INTERVAL"),
		S3Client:      s3Client,
	}

	log.Printf("[local] downloading DB from s3://%s …", bucket)
	if err := startup.InitDB(ctx, startupCfg); err != nil {
		log.Fatalf("init db: %v", err)
	}

	startup.StartDBWatcher(ctx, startupCfg)

	router := api.NewRouter(api.Config{
		S3Client:    s3Client,
		ImageDomain: imageDomain,
		StartupCfg:  startupCfg,
	})

	log.Printf("[local] listening on :%s", port)
	if err := http.ListenAndServe(":"+port, router); err != nil {
		log.Fatal(err)
	}
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
