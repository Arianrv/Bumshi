// Command bumshid is the Bumshi control-plane service that runs on the VPS.
//
// It terminates behind Caddy (which handles public TLS on 443) and exposes
// health, readiness, version, and metrics endpoints. Proxy modules — the
// generic web proxy, YouTube, and Telegram — are mounted in later milestones.
//
// Configuration is read entirely from BUMSHI_* environment variables; see
// internal/config and .env.example.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bumshi/bumshi/server/internal/auth"
	"github.com/bumshi/bumshi/server/internal/config"
	"github.com/bumshi/bumshi/server/internal/logging"
	"github.com/bumshi/bumshi/server/internal/server"
	"github.com/bumshi/bumshi/server/internal/version"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hash-password":
			if err := hashPasswordCmd(); err != nil {
				fmt.Fprintf(os.Stderr, "bumshid: %v\n", err)
				os.Exit(1)
			}
			return
		case "version", "--version", "-v":
			info := version.Get()
			fmt.Printf("bumshid %s (commit %s, %s)\n", info.Version, info.Commit, info.GoVersion)
			return
		default:
			fmt.Fprintf(os.Stderr, "bumshid: unknown command %q\n\nusage: bumshid [hash-password|version]\n", os.Args[1])
			os.Exit(2)
		}
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "bumshid: fatal: %v\n", err)
		os.Exit(1)
	}
}

// hashPasswordCmd reads a password from stdin and prints its PBKDF2 hash for use
// as BUMSHI_ADMIN_PASSWORD_HASH.
func hashPasswordCmd() error {
	fmt.Fprint(os.Stderr, "Enter admin password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read password: %w", err)
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return errors.New("password must not be empty")
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	fmt.Println(hash)
	return nil
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := logging.New(os.Stdout, cfg)
	info := version.Get()
	logger.Info("starting bumshid",
		"version", info.Version,
		"commit", info.Commit,
		"go", info.GoVersion,
		"env", string(cfg.Env),
		"listen_addr", cfg.ListenAddr,
		"metrics_addr", cfg.MetricsAddr,
		"access_log", cfg.AccessLog,
	)
	if cfg.AccessLog && cfg.IsProduction() {
		logger.Warn("access logging is ENABLED in production; disable BUMSHI_ACCESS_LOG before public release")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := server.New(cfg, logger).Run(ctx); err != nil {
		return fmt.Errorf("run server: %w", err)
	}
	logger.Info("bumshid stopped cleanly")
	return nil
}
