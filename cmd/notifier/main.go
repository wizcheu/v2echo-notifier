package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"v2echo-notifier/internal/assistant"
	"v2echo-notifier/web"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:8282", "HTTP listen address")
	dir := flag.String("data", "./data", "private persistent data directory")
	printAdmin := flag.Bool("print-admin-token", false, "print the existing local management token and exit")
	flag.Parse()
	if *printAdmin {
		raw, err := os.ReadFile(filepath.Join(*dir, "admin-token"))
		if err != nil {
			slog.Error("read management token", "error", err)
			os.Exit(1)
		}
		fmt.Print(string(raw))
		return
	}
	accounts, err := assistant.OpenAccounts(*dir)
	if err != nil {
		slog.Error("open data directory", "error", err)
		os.Exit(1)
	}
	defer accounts.Close()
	browser, err := assistant.NewBrowserService(os.Getenv("ECHO_BROWSER_CONTROL_URL"), os.Getenv("ECHO_BROWSER_DESKTOP_URL"), os.Getenv("ECHO_BROWSER_TOKEN"))
	if err != nil {
		slog.Error("initialize browser service", "error", err)
		os.Exit(1)
	}
	accounts.SetBrowserService(browser)
	ui, err := assistant.NewServer(accounts, web.Assets(), *dir)
	if err != nil {
		slog.Error("initialize management authentication", "error", err)
		os.Exit(1)
	}
	ui.SecureCookies = os.Getenv("ECHO_SECURE_COOKIE") == "true"
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan struct{})
	go func() { ui.Accounts.Run(ctx); close(done) }()
	server := &http.Server{Addr: *addr, Handler: ui.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	keyPath, _ := filepath.Abs(filepath.Join(*dir, "admin-token"))
	slog.Info("V2Echo notifier listening", "address", *addr, "admin_token_file", keyPath)
	if err = server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("HTTP server stopped", "error", err)
		stop()
	}
	stop()
	<-done
}
