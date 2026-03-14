// main.go — Lambda entrypoint for pfsrd2-data-api.
//
// On cold start:
//   1. Load AWS config
//   2. Download/verify SQLite DB from S3 (ETag check)
//   3. Open SQLite connection
//   4. Start background DB watcher
//   5. Hand off to chi router via Lambda proxy adapter
package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	chiproxy "github.com/awslabs/aws-lambda-go-api-proxy/chi"

	"github.com/521studios/pfsrd2-data-api/internal/api"
	apps3 "github.com/521studios/pfsrd2-data-api/internal/s3"
	"github.com/521studios/pfsrd2-data-api/internal/startup"
)

func main() {
	ctx := context.Background()

	bucket := mustEnv("BUCKET_NAME")
	imageDomain := envOrDefault("IMAGE_DOMAIN", "images.pfsrd2.521studios.com")

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

	log.Printf("[cold start] downloading DB from s3://%s …", bucket)
	if err := startup.InitDB(ctx, startupCfg); err != nil {
		log.Fatalf("init db: %v", err)
	}

	startup.StartDBWatcher(ctx, startupCfg)

	router := api.NewRouter(api.Config{
		S3Client:    s3Client,
		ImageDomain: imageDomain,
		StartupCfg:  startupCfg,
	})

	adapter := chiproxy.NewV2(router)
	lambda.Start(adapter.ProxyWithContextV2)
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("required env var %s not set", key)
	}
	return v
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
