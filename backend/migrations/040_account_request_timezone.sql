ALTER TABLE openai_accounts
    ADD COLUMN request_timezone TEXT NOT NULL DEFAULT ''
    CHECK (request_timezone IN ('', 'Asia/Singapore', 'Asia/Tokyo', 'Asia/Seoul', 'America/Los_Angeles', 'America/New_York', 'Europe/London'));
