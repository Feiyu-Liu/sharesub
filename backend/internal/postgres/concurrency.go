package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sharesub/sharesub/backend/internal/domain"
)

const planConcurrencyPolicySQL = `jsonb_build_object('enabled',p.concurrency_enabled,'members',(
    SELECT COALESCE(jsonb_agg(jsonb_build_object('user_id',cm.user_id,'username',cu.username,'base_limit',cm.concurrency_base,'max_concurrency',cm.concurrency_max) ORDER BY cm.created_at,cm.id), '[]'::jsonb)
    FROM plan_members cm JOIN users cu ON cu.id=cm.user_id WHERE cm.plan_id=p.id AND cm.status='active'))`

func (s *Store) PlanConcurrencyContext(ctx context.Context, planID, userID string) (domain.PlanConcurrencyContext, error) {
	var out domain.PlanConcurrencyContext
	err := s.pool.QueryRow(ctx, `SELECT p.id,p.owner_user_id,COALESCE(p.account_id,''),p.account_binding_generation,p.status,COALESCE(a.max_concurrency,0),`+planConcurrencyPolicySQL+`
        FROM shared_plans p JOIN plan_members viewer ON viewer.plan_id=p.id AND viewer.user_id=$2 AND viewer.status='active'
        LEFT JOIN openai_accounts a ON a.id=p.account_id WHERE p.id=$1`, planID, userID).Scan(&out.Plan.ID, &out.Plan.OwnerUserID, &out.Plan.AccountID, &out.Plan.AccountBindingGeneration, &out.Plan.Status, &out.AccountMax, &out.Policy)
	return out, mapError(err)
}

func (s *Store) UpdatePlanConcurrency(ctx context.Context, planID, ownerID string, policy domain.ConcurrencyPolicy, event domain.AuditEvent) (resultErr error) {
	defer func() { resultErr = mapError(resultErr) }()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owner, status string
	var accountID *string
	if err := tx.QueryRow(ctx, `SELECT owner_user_id,status,account_id FROM shared_plans WHERE id=$1 FOR UPDATE`, planID).Scan(&owner, &status, &accountID); err != nil {
		return mapError(err)
	}
	if owner != ownerID {
		return domain.ErrForbidden
	}
	if status != domain.StatusActive {
		return domain.ErrInvalidInput
	}
	limit := 0
	if accountID != nil {
		if err := tx.QueryRow(ctx, `SELECT max_concurrency FROM openai_accounts WHERE id=$1 FOR UPDATE`, *accountID).Scan(&limit); err != nil {
			return err
		}
	}
	rows, err := tx.Query(ctx, `SELECT user_id FROM plan_members WHERE plan_id=$1 AND status='active' FOR UPDATE`, planID)
	if err != nil {
		return err
	}
	users := map[string]bool{}
	for rows.Next() {
		var user string
		if err := rows.Scan(&user); err != nil {
			rows.Close()
			return err
		}
		users[user] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if len(users) != len(policy.Members) {
		return domain.ErrConflict
	}
	reserved := 0
	for _, m := range policy.Members {
		if !users[m.UserID] {
			return domain.ErrConflict
		}
		delete(users, m.UserID)
		if m.BaseLimit < 0 || m.BaseLimit > 100 || m.MaxConcurrency < 0 || m.MaxConcurrency > 100 || (m.MaxConcurrency > 0 && m.MaxConcurrency < m.BaseLimit) {
			return domain.ErrInvalidInput
		}
		reserved += m.BaseLimit
	}
	if policy.Enabled && (limit <= 0 || reserved > limit) {
		return domain.ErrConcurrencyConfiguration
	}
	if _, err := tx.Exec(ctx, `UPDATE shared_plans SET concurrency_enabled=$2,updated_at=$3 WHERE id=$1`, planID, policy.Enabled, event.CreatedAt); err != nil {
		return err
	}
	for _, m := range policy.Members {
		if _, err := tx.Exec(ctx, `UPDATE plan_members SET concurrency_base=$3,concurrency_max=$4,updated_at=$5 WHERE plan_id=$1 AND user_id=$2 AND status='active'`, planID, m.UserID, m.BaseLimit, m.MaxConcurrency, event.CreatedAt); err != nil {
			return err
		}
	}
	if err := insertAuditEvent(ctx, tx, event); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Called while holding the account/plan locks of the configuration mutation.
func validatePlanConcurrencyCapacity(ctx context.Context, tx pgx.Tx, planID string, accountMax int) error {
	var enabled bool
	var reserved int64
	err := tx.QueryRow(ctx, `SELECT p.concurrency_enabled,COALESCE((SELECT sum(m.concurrency_base) FROM plan_members m WHERE m.plan_id=p.id AND m.status='active'),0) FROM shared_plans p WHERE p.id=$1`, planID).Scan(&enabled, &reserved)
	if err != nil {
		return err
	}
	if enabled && (accountMax <= 0 || int64(accountMax) < reserved) {
		return domain.ErrConcurrencyConfiguration
	}
	return nil
}

func (s *Store) SaveConcurrencyMinutes(ctx context.Context, values []domain.ConcurrencyMinute) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	batch := &pgx.Batch{}
	for _, v := range values {
		// Removed accounts/plans may still have pending telemetry. Do not
		// resurrect them or prevent unrelated history from being persisted.
		batch.Queue(`INSERT INTO account_concurrency_minutes(instance_id,account_id,plan_id,binding_generation,bucket_start,observed_seconds,peak,members)
            SELECT $1,a.id,p.id,$4,$5,$6,$7,$8::jsonb FROM openai_accounts a,shared_plans p WHERE a.id=$2 AND p.id=$3
            ON CONFLICT(instance_id,account_id,plan_id,binding_generation,bucket_start) DO UPDATE SET observed_seconds=EXCLUDED.observed_seconds,peak=EXCLUDED.peak,members=EXCLUDED.members`, v.InstanceID, v.AccountID, v.PlanID, v.Generation, v.BucketStart, v.ObservedSeconds, v.Peak, v.Members)
	}
	results := tx.SendBatch(ctx, batch)
	if err := results.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) ConcurrencyMinutes(ctx context.Context, planID, accountID string, generation int64, start, end time.Time) ([]domain.ConcurrencyMinute, error) {
	out := make([]domain.ConcurrencyMinute, 0)
	rows, err := s.pool.Query(ctx, `SELECT instance_id,account_id,plan_id,binding_generation,bucket_start,observed_seconds,peak,members FROM account_concurrency_minutes WHERE plan_id=$1 AND account_id=$2 AND binding_generation=$3 AND bucket_start>=$4 AND bucket_start<=$5 ORDER BY bucket_start,instance_id`, planID, accountID, generation, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var v domain.ConcurrencyMinute
		if err := rows.Scan(&v.InstanceID, &v.AccountID, &v.PlanID, &v.Generation, &v.BucketStart, &v.ObservedSeconds, &v.Peak, &v.Members); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
