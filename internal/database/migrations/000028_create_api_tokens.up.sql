CREATE TABLE IF NOT EXISTS api_tokens (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL,
    masked_key TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_used_at DATETIME,
    revoked_at DATETIME
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_api_tokens_token_hash
    ON api_tokens(token_hash);

CREATE UNIQUE INDEX IF NOT EXISTS ux_api_tokens_name
    ON api_tokens(name);
