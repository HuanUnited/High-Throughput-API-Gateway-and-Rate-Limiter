-- Drop trigger first
DROP TRIGGER IF EXISTS update_clients_updated_at ON clients;

-- Drop function
DROP FUNCTION IF EXISTS update_updated_at_column();

-- Drop table
DROP TABLE IF EXISTS clients;