INSERT INTO settings (key, value, updated_at)
VALUES (
    'openai_messages_dispatch_default_target',
    'gpt-5.6-sol',
    CURRENT_TIMESTAMP
)
ON CONFLICT (key) DO NOTHING;
