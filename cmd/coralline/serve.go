package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"coralline/app/auth"
	"coralline/app/config"
	"coralline/app/httpserver"
	"coralline/app/logging"
	"coralline/app/modules"
	"coralline/app/sqlitestore"
	"coralline/app/storage"
	"coralline/app/tlsutil"
)

// legacyAdminCredentialPath is where the pre-token admin credential hash
// used to live. It is only checked so the bootstrap notice can say it no
// longer works.
const legacyAdminCredentialPath = "./data/admin-credential.hash"

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	// A missing file is the zero-config path, so a default location is safe:
	// without the file, serve runs on built-in defaults.
	configPath := fs.String("config", "config/coralline.yml", "path to the config file (optional — zero-config works without it)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := logging.New(cfg.Log.Level)

	// --- Storage (filesystem only in this prototype; S3 is a later milestone) ---
	blobs, err := storage.NewFSStore(cfg.Storage.Path)
	if err != nil {
		return fmt.Errorf("opening blob store: %w", err)
	}

	// --- SQLite (WAL mode, embedded migrations applied on open) ---
	db, err := sqlitestore.Open(cfg.Index.Path)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	// --- Admin token bootstrap (PRD FR-4.1): minted whenever no admin token is active ---
	plaintext, err := auth.EnsureAdminToken(ctx, db)
	if err != nil {
		return fmt.Errorf("bootstrapping admin token: %w", err)
	}
	if plaintext != "" {
		legacy := ""
		if _, err := os.Stat(legacyAdminCredentialPath); err == nil {
			legacy = legacyAdminCredentialPath
		}
		auth.PrintAdminBootstrapNotice(plaintext, legacy)
	}

	// Deferred after db.Close, so they flush before the database closes.
	attributor := sqlitestore.NewDownloadAttributor(db, logger)
	defer attributor.Close()
	lastUsed := sqlitestore.NewLastUsedRecorder(db, logger)
	defer lastUsed.Close()

	publisher := &modules.Publisher{Repo: db, Blobs: blobs, Log: logger, MaxUpload: cfg.Limits.MaxUploadBytes}
	go modules.RunReaper(ctx, db, blobs, logger, 30*time.Second, 15*time.Minute)

	trustedProxies := map[string]bool{}
	for _, p := range cfg.Auth.TrustedProxies {
		trustedProxies[p] = true
	}
	verifier := &auth.Verifier{
		Store:          db,
		Semaphore:      auth.NewArgonSemaphore(4), // PRD SR-3.3
		Limiter:        auth.NewIPRateLimiter(20, time.Minute, 10_000),
		TrustedProxies: trustedProxies,
		LastUsed:       lastUsed,
		Logger:         logger,
	}

	if !cfg.Auth.RequireLogin {
		logger.Warn("require_login is false: the browse UI and module downloads are anonymous")
	}

	handler, err := httpserver.New(&httpserver.Deps{
		DB:           db,
		Blobs:        blobs,
		Publisher:    publisher,
		Verifier:     verifier,
		Attributor:   attributor,
		Logger:       logger,
		RequireLogin: cfg.Auth.RequireLogin,
		MaxUpload:    cfg.Limits.MaxUploadBytes,
	})
	if err != nil {
		return fmt.Errorf("building server: %w", err)
	}

	// --- Admin API: loopback only, separate listener (PRD FR-4.3) ---
	adminHandler := httpserver.NewAdminServer(db, verifier, logger)
	adminLis, err := net.Listen("tcp", cfg.AdminInternalAddr)
	if err != nil {
		return fmt.Errorf("binding admin listener: %w", err)
	}
	adminSrv := &http.Server{
		Handler:           adminHandler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}
	go func() {
		logger.Info("admin API listening", "addr", cfg.AdminInternalAddr)
		if err := adminSrv.Serve(adminLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("admin server stopped", "error", err)
		}
	}()

	// --- Public listener: TLS by default (PRD SR-4.1), local-evaluation caveat documented in tlsutil ---
	lis, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("binding listener: %w", err)
	}

	if !cfg.TLS.Disabled {
		cert, err := tlsutil.EnsureSelfSigned(cfg.TLS.CertFile, cfg.TLS.KeyFile)
		if err != nil {
			return fmt.Errorf("preparing TLS certificate: %w", err)
		}
		lis = httpserver.NewTLSListener(lis, cert)
		logger.Info("serving over TLS", "addr", cfg.Listen)
	} else {
		logger.Warn("TLS disabled — local evaluation only, terraform init against a remote host requires HTTPS")
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16,
	}

	logger.Info("coralline listening", "addr", cfg.Listen, "storage", cfg.Storage.Backend, "require_login", cfg.Auth.RequireLogin)
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	select {
	case err := <-serveErr:
		_ = adminSrv.Close()
		return err
	case <-ctx.Done():
	}

	// Let in-flight requests (including uploads) finish, then return so the
	// deferred recorder flushes and database close run.
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("public server shutdown", "error", err)
	}
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("admin server shutdown", "error", err)
	}
	return nil
}
