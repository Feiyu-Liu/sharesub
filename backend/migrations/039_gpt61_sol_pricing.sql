-- Official GPT-6.1 Sol Standard rates supplied on 2026-09-30.
-- Preserve historical versions and existing administrator prices.
WITH addition AS (
    SELECT '{
      "model": "gpt-6.1-sol",
      "standard": {
        "input": 2000000,
        "output": 10000000,
        "cache_read": 100000,
        "cache_write": 2500000,
        "image_input": 2000000,
        "image_output": 10000000
      },
      "long_context_tokens": 272000,
      "long_input_bps": 20000,
      "long_output_bps": 15000
    }'::jsonb AS model
), current_version AS (
    SELECT p.id, p.config
    FROM current_pricing c JOIN pricing_versions p ON p.id = c.version_id
    WHERE c.singleton
    FOR UPDATE OF c
), published AS (
    INSERT INTO pricing_versions(published_by, reason, config)
    SELECT 'system', '新增 GPT-6.1 Sol 标准计价；保留现有价格和倍率',
           jsonb_set(v.config, '{models}', v.config->'models' || a.model)
    FROM current_version v, addition a
    WHERE NOT EXISTS (
        SELECT 1 FROM jsonb_array_elements(v.config->'models') existing
        WHERE existing->>'model' = a.model->>'model'
    )
    RETURNING id
)
UPDATE current_pricing SET version_id = published.id FROM published WHERE singleton;
