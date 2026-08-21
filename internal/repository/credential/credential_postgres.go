package credential

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aarondl/sqlboiler/v4/boil"

	dbpg "github.com/thelemail/thaumaste/internal/db/postgres"
	"github.com/thelemail/thaumaste/internal/entity"
	"github.com/thelemail/thaumaste/internal/pkg/postgres"
	"github.com/thelemail/thaumaste/internal/repository"
)

type repo struct {
	db *postgres.Client
}

func New(db *postgres.Client) repository.Credential {
	return &repo{db: db}
}

func (r *repo) Upsert(ctx context.Context, scope entity.TenantScope, in entity.Credential) error {
	row := dbpg.UserCredential{
		TenantID:  scope.ID().String(),
		UserID:    in.UserID,
		Algorithm: in.Algorithm,
		Params:    in.Params,
		Salt:      in.Salt,
		Hash:      in.Hash,
	}
	err := row.Upsert(ctx, r.db.Querier(ctx), true,
		[]string{dbpg.UserCredentialColumns.TenantID, dbpg.UserCredentialColumns.UserID},
		boil.Whitelist(
			dbpg.UserCredentialColumns.Algorithm,
			dbpg.UserCredentialColumns.Params,
			dbpg.UserCredentialColumns.Salt,
			dbpg.UserCredentialColumns.Hash,
			dbpg.UserCredentialColumns.UpdatedAt,
		),
		boil.Infer())
	if err != nil {
		return fmt.Errorf("repository: upsert credential: %w", err)
	}
	return nil
}

func (r *repo) Get(ctx context.Context, scope entity.TenantScope, userID string) (entity.Credential, error) {
	row, err := dbpg.UserCredentials(
		dbpg.UserCredentialWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UserCredentialWhere.UserID.EQ(userID),
	).One(ctx, r.db.Querier(ctx))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return entity.Credential{}, repository.ErrCredentialNotFound
		}
		return entity.Credential{}, fmt.Errorf("repository: get credential: %w", err)
	}
	return entity.Credential{
		UserID:    row.UserID,
		Algorithm: row.Algorithm,
		Params:    row.Params,
		Salt:      row.Salt,
		Hash:      row.Hash,
	}, nil
}

func (r *repo) Delete(ctx context.Context, scope entity.TenantScope, userID string) error {
	_, err := dbpg.UserCredentials(
		dbpg.UserCredentialWhere.TenantID.EQ(scope.ID().String()),
		dbpg.UserCredentialWhere.UserID.EQ(userID),
	).DeleteAll(ctx, r.db.Querier(ctx))
	if err != nil {
		return fmt.Errorf("repository: delete credential: %w", err)
	}
	return nil
}
