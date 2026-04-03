-- Remove job_title and phone columns from users table
ALTER TABLE users DROP COLUMN IF EXISTS job_title;
ALTER TABLE users DROP COLUMN IF EXISTS phone;
