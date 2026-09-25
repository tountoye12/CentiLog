package main

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type user struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type credentials struct {
	user
	PasswordHash string
}

type service struct {
	Name         string    `json:"name"`
	RetentionDays int      `json:"retention_days"`
	CreatedAt    time.Time `json:"created_at"`
}

type repository interface {
	CredentialsByEmail(context.Context, string) (credentials, error)
	ServicesForUser(context.Context, int64, string) ([]service, error)
	CreateService(context.Context, string, string, int) (service, error)
	UpdateRetention(context.Context, string, int) error
	CreateUser(context.Context, string, string, string, []string) (user, error)
}

type postgresRepository struct {
	db *sql.DB
}

func (repository postgresRepository) CredentialsByEmail(ctx context.Context, email string) (credentials, error) {
	var result credentials
	err := repository.db.QueryRowContext(ctx,
		`SELECT id, email, password_hash, role FROM users WHERE email = $1`, email,
	).Scan(&result.ID, &result.Email, &result.PasswordHash, &result.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return credentials{}, errInvalidCredentials
	}
	if err != nil {
		return credentials{}, errors.New("query user credentials")
	}
	return result, nil
}

func (repository postgresRepository) ServicesForUser(ctx context.Context, userID int64, role string) ([]service, error) {
	rows, err := repository.db.QueryContext(ctx, `
		SELECT s.name, s.retention_days, s.created_at
		FROM services AS s
		LEFT JOIN user_services AS us
			ON us.service_name = s.name AND us.user_id = $1
		WHERE $2 = 'admin' OR us.user_id IS NOT NULL
		ORDER BY s.name`, userID, role)
	if err != nil {
		return nil, errors.New("query visible services")
	}
	defer rows.Close()

	services := make([]service, 0)
	for rows.Next() {
		var item service
		if err := rows.Scan(&item.Name, &item.RetentionDays, &item.CreatedAt); err != nil {
			return nil, errors.New("read visible services")
		}
		services = append(services, item)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read visible services")
	}
	return services, nil
}

func (repository postgresRepository) CreateService(ctx context.Context, name, apiKey string, retentionDays int) (service, error) {
	var created service
	err := repository.db.QueryRowContext(ctx, `
		INSERT INTO services (name, api_key, retention_days)
		VALUES ($1, $2, $3)
		RETURNING name, retention_days, created_at`, name, apiKey, retentionDays,
	).Scan(&created.Name, &created.RetentionDays, &created.CreatedAt)
	if err != nil {
		return service{}, err
	}
	return created, nil
}

func (repository postgresRepository) UpdateRetention(ctx context.Context, name string, retentionDays int) error {
	result, err := repository.db.ExecContext(ctx,
		`UPDATE services SET retention_days = $1 WHERE name = $2`, retentionDays, name,
	)
	if err != nil {
		return errors.New("update service retention")
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return errors.New("check service update")
	}
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (repository postgresRepository) CreateUser(ctx context.Context, email, passwordHash, role string, serviceNames []string) (user, error) {
	transaction, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		return user{}, errors.New("begin user creation")
	}
	defer transaction.Rollback()

	var created user
	err = transaction.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role)
		VALUES ($1, $2, $3)
		RETURNING id, email, role`, email, passwordHash, role,
	).Scan(&created.ID, &created.Email, &created.Role)
	if err != nil {
		return user{}, err
	}

	if role == "viewer" {
		for _, serviceName := range serviceNames {
			if _, err := transaction.ExecContext(ctx,
				`INSERT INTO user_services (user_id, service_name) VALUES ($1, $2)`, created.ID, serviceName,
			); err != nil {
				return user{}, err
			}
		}
	}
	if err := transaction.Commit(); err != nil {
		return user{}, errors.New("commit user creation")
	}
	return created, nil
}

func isUniqueViolation(err error) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == "23505"
}
