package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/biubiubiu125/icloud-mail/internal/config"
	"github.com/biubiubiu125/icloud-mail/internal/domain"
	"github.com/biubiubiu125/icloud-mail/internal/httpapi"
	"github.com/biubiubiu125/icloud-mail/internal/mailbox"
	"github.com/biubiubiu125/icloud-mail/internal/protocol"
	"github.com/biubiubiu125/icloud-mail/internal/store"
)

func main() {
	configPath := flag.String("config", "config.json", "配置文件路径，环境变量优先")
	flag.Parse()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("config invalid")
		os.Exit(1)
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		logger.Error("database url is empty")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("database unavailable")
		os.Exit(1)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		logger.Error("migrate failed")
		os.Exit(1)
	}
	changed, err := db.NormalizeInterruptedDeletes(ctx)
	if err != nil {
		logger.Error("normalize deletes failed")
		os.Exit(1)
	}
	if changed > 0 {
		logger.Warn("interrupted remote deletes marked unknown", "count", changed)
	}
	if err := seed(ctx, db, cfg); err != nil {
		logger.Error("seed failed", "error", err.Error())
		os.Exit(1)
	}
	svc := mailbox.NewService(db, nil)
	svc.Run(ctx, logger)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.New(cfg, db, svc, logger).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       3 * time.Minute,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	logger.Info("listening", "addr", cfg.HTTPAddr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped")
		os.Exit(1)
	}
}

func seed(ctx context.Context, db *store.Store, cfg config.Config) error {
	username := strings.TrimSpace(cfg.AdminUsername)
	password := cfg.AdminPassword
	if username != "" || strings.TrimSpace(password) != "" {
		count, err := db.AdminCount(ctx)
		if err != nil {
			return err
		}
		if err := validateAdminSeed(username, password, count); err != nil {
			return err
		}
		if count == 0 {
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			id, err := protocol.RandomID()
			if err != nil {
				return err
			}
			if err := db.CreateAdmin(ctx, domain.Admin{ID: id, Username: username, PasswordHash: string(hash), CreatedAt: time.Now()}); err != nil {
				return err
			}
		}
	}
	return db.EnsureAPIKey(ctx, cfg.APIKey)
}

func validateAdminSeed(username, password string, existing int) error {
	if existing > 0 || (username == "" && strings.TrimSpace(password) == "") {
		return nil
	}
	if len(username) < 3 || len(password) < 8 {
		return errors.New("admin username must be at least 3 characters and password at least 8")
	}
	return nil
}
