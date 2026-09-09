-- 若 001 仍是旧版（只建了 sessions），这里删掉后重建 conversations。
DROP TABLE IF EXISTS sessions CASCADE;


CREATE TABLE IF NOT EXISTS conversations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users (id),
    title TEXT NOT NULL DEFAULT '',
    messages JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS conversations_user_id_updated_at_idx
    ON conversations (user_id, updated_at DESC);

CREATE INDEX IF NOT EXISTS conversations_updated_at_idx
    ON conversations (updated_at);
