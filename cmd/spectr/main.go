package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/Abaddollyon/spectr/pkg/engine/api"
	"github.com/Abaddollyon/spectr/pkg/engine/collector/openclaw"
	"github.com/Abaddollyon/spectr/pkg/engine/store/sqlite"
)

const version = "0.1.0-dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("spectr %s\n", version)
		return
	}

	// Config
	port := envOrInt("SPECTR_PORT", 9099)
	dbPath := envOr("SPECTR_DB", "spectr.db")

	log.Printf("spectr %s starting — port=%d db=%s", version, port, dbPath)

	// Init store
	store, err := sqlite.New(dbPath)
	if err != nil {
		log.Fatalf("failed to open store: %v", err)
	}
	defer store.Close()
	log.Println("store: sqlite initialized")

	// Init collector
	collector := &openclaw.Collector{}
	paths, err := collector.Discover()
	if err != nil {
		log.Printf("collector: discovery warning: %v", err)
	} else {
		log.Printf("collector: discovered %d OpenClaw session files", len(paths))
	}

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start collector in background
	go func() {
		log.Println("collector: starting OpenClaw watcher")
		if err := collector.Start(ctx, store); err != nil {
			log.Printf("collector: stopped: %v", err)
		}
	}()

	// Start API server (blocks until ctx cancelled)
	srv := api.NewServer(store, port)
	log.Printf("api: listening on :%d", port)

	go func() {
		if err := srv.Start(ctx); err != nil {
			log.Fatalf("api: %v", err)
		}
	}()

	// Wait for shutdown signal
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("shutting down...")
	cancel()
	log.Println("spectr stopped")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envOrInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}
