package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sharesub/sharesub/backend/internal/domain"
)

func TestConcurrencyPolicyAndHistoryPersistence(t *testing.T) {
	store := membershipTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	_, err := store.pool.Exec(ctx, `
        INSERT INTO openai_accounts(id,owner_user_id,name,email,chatgpt_account_id,access_token_ciphertext,refresh_token_ciphertext,token_expires_at,status,max_concurrency)
        VALUES('account','owner','test','owner@example.test','chatgpt','access','refresh',now()+interval '1 day','active',12);
        INSERT INTO shared_plans(id,owner_user_id,account_id,name,allocation_mode,account_binding_generation,account_bound_at) VALUES('plan','owner','account','concurrency','shared',1,now());
        INSERT INTO plan_members(id,plan_id,user_id,role,status,share_basis_points) VALUES('owner-member','plan','owner','owner','active',0),('test-member','plan','member','member','active',0);
        INSERT INTO api_keys(id,user_id,name,key_prefix,key_hash,strategy,status) VALUES('key','member','test','sk','hash','priority','active');
        INSERT INTO api_key_plans(api_key_id,plan_id,priority,enabled) VALUES('key','plan',1,true);`)
	if err != nil {
		t.Fatal(err)
	}
	info, err := store.PlanConcurrencyContext(ctx, "plan", "member")
	if err != nil {
		t.Fatal(err)
	}
	if info.Policy.Enabled || len(info.Policy.Members) != 2 {
		t.Fatalf("migration changed default: %+v", info)
	}
	if _, err := store.PlanConcurrencyContext(ctx, "plan", "new"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("nonmember read: %v", err)
	}
	policy := domain.ConcurrencyPolicy{Enabled: true, Members: []domain.MemberConcurrencyLimit{{UserID: "owner", BaseLimit: 2, MaxConcurrency: 6}, {UserID: "member", BaseLimit: 2, MaxConcurrency: 8}}}
	if err := store.UpdatePlanConcurrency(ctx, "plan", "member", policy, membershipAudit("forbidden", now)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("nonowner update: %v", err)
	}
	if err := store.UpdatePlanConcurrency(ctx, "plan", "owner", policy, membershipAudit("save", now)); err != nil {
		t.Fatal(err)
	}
	routes, err := store.ResolveGatewayRoutes(ctx, []byte("hash"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes.Candidates) != 1 || !routes.Candidates[0].ConcurrencyPolicy.Enabled || len(routes.Candidates[0].ConcurrencyPolicy.Members) != 2 {
		t.Fatalf("gateway did not receive policy: %+v", routes)
	}
	invalid := domain.ConcurrencyPolicy{Enabled: true, Members: []domain.MemberConcurrencyLimit{{UserID: "owner", BaseLimit: 10, MaxConcurrency: 10}, {UserID: "member", BaseLimit: 10, MaxConcurrency: 10}}}
	if err := store.UpdatePlanConcurrency(ctx, "plan", "owner", invalid, membershipAudit("invalid", now)); !errors.Is(err, domain.ErrConcurrencyConfiguration) {
		t.Fatalf("oversubscribed base: %v", err)
	}
	account, err := store.AccountByID(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 3} {
		account.MaxConcurrency = limit
		if _, err := store.UpdateAccountConfig(ctx, "owner", account, membershipAudit("lower", now)); !errors.Is(err, domain.ErrConcurrencyConfiguration) {
			t.Fatalf("lower account max to %d: %v", limit, err)
		}
	}
	stale := domain.ConcurrencyPolicy{Enabled: true, Members: policy.Members[:1]}
	if err := store.UpdatePlanConcurrency(ctx, "plan", "owner", stale, membershipAudit("stale", now)); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("partial member list: %v", err)
	}
	sample := domain.ConcurrencyMinute{InstanceID: "process-1", AccountID: "account", PlanID: "plan", Generation: 1, BucketStart: now, ObservedSeconds: 30, Peak: 2, Members: []domain.ConcurrencyMemberSample{{UserID: "member", Username: "member", RequestSeconds: 40}}}
	if err := store.SaveConcurrencyMinutes(ctx, []domain.ConcurrencyMinute{sample}); err != nil {
		t.Fatal(err)
	}
	sample.ObservedSeconds = 60
	sample.Members[0].RequestSeconds = 70
	if err := store.SaveConcurrencyMinutes(ctx, []domain.ConcurrencyMinute{sample, sample}); err != nil {
		t.Fatal(err)
	}
	// A fresh Store instance sees persisted history; repeated flushes replace it.
	restarted := &Store{pool: store.pool}
	records, err := restarted.ConcurrencyMinutes(ctx, "plan", "account", 1, now, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ObservedSeconds != 60 || records[0].Members[0].RequestSeconds != 70 {
		t.Fatalf("history duplicated/lost: %+v", records)
	}
	records, err = store.ConcurrencyMinutes(ctx, "plan", "account", 2, now, now.Add(time.Minute))
	if err != nil || len(records) != 0 {
		t.Fatalf("old binding leaked: %+v %v", records, err)
	}
	if err := store.RemovePlanMember(ctx, "plan", "owner", "test-member", membershipAudit("remove", now)); err != nil {
		t.Fatal(err)
	}
	var base, limit int
	if err := store.pool.QueryRow(ctx, `SELECT concurrency_base,concurrency_max FROM plan_members WHERE id='test-member'`).Scan(&base, &limit); err != nil {
		t.Fatal(err)
	}
	if base != 0 || limit != 0 {
		t.Fatal("removed member retained reservations")
	}
}
