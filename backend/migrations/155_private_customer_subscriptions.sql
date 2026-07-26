-- Private customer subscriptions are intentionally isolated from Sub2API
-- users, API keys, groups, payment orders, and billing subscriptions.
CREATE TABLE IF NOT EXISTS private_customer_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(120) NOT NULL,
    subscription_type VARCHAR(50) NOT NULL,
    amount_cents BIGINT NOT NULL DEFAULT 0,
    expires_on DATE NOT NULL,
    reminder_sent_for_expiry DATE NULL,
    reminder_sent_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ NULL,
    CONSTRAINT private_customer_subscriptions_amount_nonnegative
        CHECK (amount_cents >= 0)
);

CREATE INDEX IF NOT EXISTS private_customer_subscriptions_name_idx
    ON private_customer_subscriptions (name);

CREATE INDEX IF NOT EXISTS private_customer_subscriptions_type_idx
    ON private_customer_subscriptions (subscription_type);

CREATE INDEX IF NOT EXISTS private_customer_subscriptions_expires_on_idx
    ON private_customer_subscriptions (expires_on);

CREATE INDEX IF NOT EXISTS private_customer_subscriptions_reminder_due_idx
    ON private_customer_subscriptions (expires_on, reminder_sent_for_expiry)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS private_customer_subscriptions_deleted_at_idx
    ON private_customer_subscriptions (deleted_at);
