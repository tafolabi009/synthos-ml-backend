-- Credits system tables for Synthos billing

-- Credit balances per user
CREATE TABLE IF NOT EXISTS credit_balances (
    id VARCHAR(255) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    balance BIGINT NOT NULL DEFAULT 0,
    lifetime_purchased BIGINT NOT NULL DEFAULT 0,
    lifetime_used BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id)
);

CREATE INDEX idx_credit_balances_user_id ON credit_balances(user_id);

-- Credit transactions (purchases, deductions, refunds)
CREATE TABLE IF NOT EXISTS credit_transactions (
    id VARCHAR(255) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type VARCHAR(50) NOT NULL, -- 'purchase', 'deduction', 'refund', 'bonus', 'expiry'
    amount BIGINT NOT NULL, -- positive for credits in, negative for credits out
    balance_after BIGINT NOT NULL,
    description TEXT,
    reference_type VARCHAR(50), -- 'validation', 'package', 'admin', 'warranty'
    reference_id VARCHAR(255), -- validation_id, package purchase id, etc.
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_credit_transactions_user_id ON credit_transactions(user_id);
CREATE INDEX idx_credit_transactions_type ON credit_transactions(type);
CREATE INDEX idx_credit_transactions_created_at ON credit_transactions(created_at DESC);
CREATE INDEX idx_credit_transactions_reference ON credit_transactions(reference_type, reference_id);

-- Credit packages available for purchase
CREATE TABLE IF NOT EXISTS credit_packages (
    id VARCHAR(255) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    credits BIGINT NOT NULL,
    price_cents BIGINT NOT NULL, -- price in cents (USD)
    currency VARCHAR(10) NOT NULL DEFAULT 'USD',
    bonus_credits BIGINT NOT NULL DEFAULT 0, -- extra credits included
    is_active BOOLEAN NOT NULL DEFAULT true,
    sort_order INT NOT NULL DEFAULT 0,
    metadata JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Credit cost configuration per operation type
CREATE TABLE IF NOT EXISTS credit_costs (
    id VARCHAR(255) PRIMARY KEY,
    operation VARCHAR(100) NOT NULL UNIQUE, -- 'validation_standard', 'validation_express', 'warranty_request'
    credits_required BIGINT NOT NULL,
    description TEXT,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Insert default credit packages
INSERT INTO credit_packages (id, name, description, credits, price_cents, bonus_credits, sort_order) VALUES
    ('pkg_starter', 'Starter', 'Get started with basic validations', 100, 9900, 0, 1),
    ('pkg_professional', 'Professional', 'For teams running regular validations', 500, 39900, 50, 2),
    ('pkg_business', 'Business', 'High-volume validation workloads', 2000, 149900, 300, 3),
    ('pkg_enterprise', 'Enterprise', 'Unlimited-scale validation with premium support', 10000, 599900, 2000, 4)
ON CONFLICT DO NOTHING;

-- Insert default credit costs per operation
INSERT INTO credit_costs (id, operation, credits_required, description) VALUES
    ('cost_val_std', 'validation_standard', 10, 'Standard priority validation job'),
    ('cost_val_exp', 'validation_express', 20, 'Express priority validation job (2x)'),
    ('cost_warranty', 'warranty_request', 5, 'Performance warranty request'),
    ('cost_revalidation', 'revalidation', 8, 'Re-validation of previously validated dataset')
ON CONFLICT DO NOTHING;
