package postgres

import (
	"context"
	"testing"
	"time"
)

func TestAccountRequestTimezoneRoundTrip(t *testing.T) {
	store := membershipTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)
	_, err := store.pool.Exec(ctx, `
        INSERT INTO openai_accounts(id,owner_user_id,name,email,chatgpt_account_id,access_token_ciphertext,refresh_token_ciphertext,token_expires_at,status)
        VALUES('account','owner','test','owner@example.test','chatgpt','access','refresh',now()+interval '1 day','active');
        INSERT INTO shared_plans(id,owner_user_id,account_id,name,allocation_mode,account_binding_generation,account_bound_at) VALUES('plan','owner','account','timezone','shared',1,now());
        INSERT INTO plan_members(id,plan_id,user_id,role,status,share_basis_points) VALUES('owner-member','plan','owner','owner','active',0);
        INSERT INTO api_keys(id,user_id,name,key_prefix,key_hash,strategy,status) VALUES('key','owner','test','sk','hash','priority','active');
        INSERT INTO api_key_plans(api_key_id,plan_id,priority,enabled) VALUES('key','plan',1,true);`)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.AccountByID(ctx, "account")
	if err != nil {
		t.Fatal(err)
	}
	if account.RequestTimezone != "" {
		t.Fatalf("default request timezone = %q, want disabled", account.RequestTimezone)
	}
	account.RequestTimezone = "Asia/Singapore"
	updated, err := store.UpdateAccountConfig(ctx, "owner", account, membershipAudit("timezone", now))
	if err != nil {
		t.Fatal(err)
	}
	if updated.RequestTimezone != "Asia/Singapore" {
		t.Fatalf("updated request timezone = %q", updated.RequestTimezone)
	}
	listed, err := store.ListAccounts(ctx, "owner")
	if err != nil || len(listed) != 1 || listed[0].RequestTimezone != "Asia/Singapore" {
		t.Fatalf("listed accounts = %+v, %v", listed, err)
	}
	routes, err := store.ResolveGatewayRoutes(ctx, []byte("hash"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes.Candidates) != 1 || routes.Candidates[0].Account.RequestTimezone != "Asia/Singapore" {
		t.Fatalf("gateway did not receive request timezone: %+v", routes)
	}
	// Reconnecting must keep the configured timezone like other account settings.
	account.ID = "replacement"
	account.RequestTimezone = ""
	account.CreatedAt = now
	reauthorized, err := store.CreateOrRotateAccountAuthorization(ctx, account, false)
	if err != nil {
		t.Fatal(err)
	}
	if reauthorized.ID != "account" || reauthorized.RequestTimezone != "Asia/Singapore" {
		t.Fatalf("reauthorization changed request timezone: %+v", reauthorized)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE openai_accounts SET request_timezone='Asia/Shanghai' WHERE id='account'`); err == nil {
		t.Fatal("database accepted an unlisted request timezone")
	}
}
