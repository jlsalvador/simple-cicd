package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jlsalvador/simple-cicd/internal/controller"
	"github.com/jlsalvador/simple-cicd/internal/k8s"
	"github.com/jlsalvador/simple-cicd/internal/version"
	"github.com/jlsalvador/simple-cicd/internal/webhook"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Printf("simple-cicd operator starting (version %s)", version.Version)

	client, err := k8s.NewClient()
	if err != nil {
		log.Fatalf("failed to create Kubernetes client: %v", err)
	}

	reconciler := controller.NewReconciler(client)
	webhookHandler := webhook.NewHandler(client, reconciler.Trigger)

	// Start the reconciliation loop in the background
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go reconciler.Run(ctx)

	// Set up the HTTP server
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "ok\nversion: %s\n", version.Version)
	})
	// All other paths are delegated to the webhook handler
	mux.Handle("/", webhookHandler)

	server := &http.Server{
		Addr:         ":9000",
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown on SIGINT / SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Printf("received signal %v - shutting down", sig)
		cancel() // stop the reconciler

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
	}()

	log.Printf("HTTP server listening on :9000")
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}
	log.Println("simple-cicd operator stopped")
}
