package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"centilog/internal/config"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "create-admin" {
		if err := createAdminCommand(os.Args[1:]); err != nil {
			log.Print(err)
			os.Exit(1)
		}
		return
	}
	if err := runServer(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func createAdminCommand(arguments []string) error {
	if len(arguments) != 3 {
		return errors.New("usage: go run ./cmd/api create-admin <email> <password>")
	}
	email := arguments[1]
	password := arguments[2]
	if len(password) < 8 || len(password) > 72 {
		return errors.New("admin password must be between 8 and 72 bytes")
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return errors.New("hash admin password")
	}
	db, err := openPostgres()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return errors.New("PostgreSQL is unavailable")
	}
	account, err := (postgresRepository{db: db}).CreateUser(ctx, email, string(passwordHash), "admin", nil)
	if err != nil {
		if isUniqueViolation(err) {
			return errors.New("a user with that email already exists")
		}
		return errors.New("could not create admin user")
	}
	log.Printf("created admin user %s (id %d)", account.Email, account.ID)
	return nil
}

func runServer() error {
	db, err := openPostgres()
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	startupCtx, cancelStartup := context.WithTimeout(ctx, 5*time.Second)
	if err := db.PingContext(startupCtx); err != nil {
		cancelStartup()
		return errors.New("PostgreSQL is unavailable")
	}
	cancelStartup()

	secret, ephemeral, err := jwtSecret()
	if err != nil {
		return err
	}
	if ephemeral {
		log.Print("WARNING: JWT_SECRET is unset; using a temporary random secret, so all tokens expire when the API restarts")
	}

	chUser, chPassword, chDatabase := clickHouseCredentials()
	clickhouse, err := newClickHouseHTTPClient(config.ClickHouseHTTPURL(), chUser, chPassword, chDatabase)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              config.GetEnv("CENTILOG_API_ADDR", ":8081"),
		Handler:           newAPIHandler(postgresRepository{db: db}, clickhouse, newJWTManager(secret)),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- errors.New("HTTP server failed")
			return
		}
		serverErrors <- nil
	}()
	log.Printf("query API listening on %s", server.Addr)

	select {
	case <-ctx.Done():
		shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return errors.New("graceful shutdown failed")
		}
		return nil
	case err := <-serverErrors:
		return err
	}
}

func openPostgres() (*sql.DB, error) {
	db, err := sql.Open("pgx", config.PostgresDSN())
	if err != nil {
		return nil, errors.New("open PostgreSQL")
	}
	db.SetMaxOpenConns(10)
	return db, nil
}

func jwtSecret() ([]byte, bool, error) {
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		return []byte(secret), false, nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, false, errors.New("generate temporary JWT secret")
	}
	return []byte(hex.EncodeToString(secret)), true, nil
}

func clickHouseCredentials() (string, string, string) {
	user := "centilog"
	password := "centilog"
	database := "centilog"
	if dsn, err := url.Parse(config.ClickHouseDSN()); err == nil {
		if dsn.User != nil {
			user = dsn.User.Username()
			password, _ = dsn.User.Password()
		}
		if name := strings.Trim(dsn.Path, "/"); name != "" {
			database = name
		}
	}
	return user, password, database
}
