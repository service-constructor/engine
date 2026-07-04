-- Index for the personal cabinet's "My orders" listing: fetch a user's orders
-- newest-first. Covers ListOrders (WHERE user_id = $1 ORDER BY created_at DESC).
CREATE INDEX IF NOT EXISTS orders_user_created_idx
    ON orders (user_id, created_at DESC);
