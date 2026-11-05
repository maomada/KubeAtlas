package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/maomada/KubeAtlas/internal/fleetsync"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	flags := flag.NewFlagSet("kubeatlas-fleet", flag.ContinueOnError)
	listenAddress := flags.String("listen", "127.0.0.1:9291", "HTTP listen address")
	dataFile := flags.String("data-file", "./fleet-data/state.json", "private Fleet snapshot state file")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}

	listener, err := net.Listen("tcp", *listenAddress)
	if err != nil {
		return fmt.Errorf("Fleet manager could not listen: %w", err)
	}
	defer listener.Close()
	if isNonLoopback(*listenAddress) {
		log.Printf("WARNING: Fleet snapshot manager is an unauthenticated development prototype listening on a non-loopback address; use only on a trusted isolated development network and never expose publicly or in production")
	} else {
		log.Printf("Fleet snapshot manager is an unauthenticated development prototype; never expose publicly or use in production")
	}

	store, err := fleetsync.OpenStore(*dataFile)
	if err != nil {
		return err
	}
	defer store.Close()

	server := &http.Server{
		Handler:           fleetsync.NewHandler(store),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.Serve(listener)
	}()
	log.Printf("Fleet snapshot manager listening at %s", listener.Addr())
	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		serveErr := <-serveResult
		if shutdownErr != nil {
			return fmt.Errorf("Fleet manager shutdown failed: %w", shutdownErr)
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		return nil
	}
}

func isNonLoopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return true
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}
