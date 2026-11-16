package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"time"
)

func (r *QuotaRepository) SetVerification(ctx context.Context, id, link string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO account_verification(account_id,url,observed_at) VALUES(?,?,?) ON CONFLICT(account_id) DO UPDATE SET url=excluded.url,observed_at=excluded.observed_at`, id, domain.SafeVerificationURL(link), time.Now().UTC().UnixNano())
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM family_active_accounts WHERE account_id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *QuotaRepository) GetVerification(ctx context.Context, id string) (*domain.VerificationRequirement, error) {
	var result domain.VerificationRequirement
	var nanos int64
	err := r.db.QueryRowContext(ctx, `SELECT url,observed_at FROM account_verification WHERE account_id=?`, id).Scan(&result.URL, &nanos)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result.URL = domain.SafeVerificationURL(result.URL)
	result.ObservedAt = time.Unix(0, nanos).UTC()
	return &result, nil
}
func (r *QuotaRepository) ClearVerificationBefore(ctx context.Context, id string, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM account_verification WHERE account_id=? AND observed_at<=?`, id, before.UnixNano())
	return err
}
