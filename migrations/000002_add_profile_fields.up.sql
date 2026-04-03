-- Add job_title and phone columns to users table
DO $$ 
BEGIN
    ALTER TABLE users ADD COLUMN IF NOT EXISTS job_title VARCHAR(100);
    ALTER TABLE users ADD COLUMN IF NOT EXISTS phone VARCHAR(20);
EXCEPTION WHEN OTHERS THEN
    NULL;
END $$;

-- Drop subscription_tier column if we want to remove it (keeping for backward compatibility)
-- The API now returns billing_type: "enterprise" instead
