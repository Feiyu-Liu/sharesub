ALTER TABLE shared_plans ADD COLUMN concurrency_enabled boolean NOT NULL DEFAULT false;
ALTER TABLE plan_members
    ADD COLUMN concurrency_base integer NOT NULL DEFAULT 0 CHECK (concurrency_base BETWEEN 0 AND 100),
    ADD COLUMN concurrency_max integer NOT NULL DEFAULT 0 CHECK (concurrency_max BETWEEN 0 AND 100),
    ADD CONSTRAINT plan_members_concurrency_limits CHECK (concurrency_max = 0 OR concurrency_max >= concurrency_base);

-- Absolute, idempotent minute snapshots. A collector incarnation never overwrites another.
CREATE TABLE account_concurrency_minutes (
    instance_id text NOT NULL,
    account_id text NOT NULL REFERENCES openai_accounts(id) ON DELETE CASCADE,
    plan_id text NOT NULL REFERENCES shared_plans(id) ON DELETE CASCADE,
    binding_generation bigint NOT NULL,
    bucket_start timestamptz NOT NULL,
    observed_seconds double precision NOT NULL CHECK (observed_seconds >= 0 AND observed_seconds <= 60.000001),
    peak integer NOT NULL CHECK (peak >= 0),
    members jsonb NOT NULL CHECK (jsonb_typeof(members) = 'array'),
    PRIMARY KEY (instance_id, account_id, plan_id, binding_generation, bucket_start)
);
CREATE INDEX account_concurrency_minutes_history ON account_concurrency_minutes(plan_id, account_id, binding_generation, bucket_start);
CREATE INDEX account_concurrency_minutes_cleanup ON account_concurrency_minutes(bucket_start);
