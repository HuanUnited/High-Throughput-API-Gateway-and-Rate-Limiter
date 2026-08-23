-- Create clients table for API key-based rate limiting
CREATE TABLE IF NOT EXISTS clients (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    api_key     VARCHAR(255) NOT NULL UNIQUE,
    rate_limit  INT NOT NULL CHECK (rate_limit > 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Create index for API key lookups (though UNIQUE constraint already creates one)
CREATE INDEX IF NOT EXISTS idx_clients_api_key ON clients(api_key);

-- Create a trigger to automatically update updated_at on row changes
CREATE OR REPLACE FUNCTION update_updated_at_column()
RETURNS TRIGGER AS $$BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;$$ LANGUAGE plpgsql;

CREATE TRIGGER update_clients_updated_at
    BEFORE UPDATE ON clients
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Insert default clients for testing (optional, remove in production)
INSERT INTO clients (api_key, rate_limit) VALUES
    ('default-key', 10),
    ('premium-key', 100),
    ('enterprise-key', 1000),
    ('test-api-key', 10000)
ON CONFLICT (api_key) DO NOTHING;