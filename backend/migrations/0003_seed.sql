-- Seed the real existing Ventatec company + admin user so login works
-- immediately after cutover from Supabase.
-- password_hash below is bcrypt("Admin123"), verified with bcrypt.CompareHashAndPassword.

INSERT INTO companies (id, name, ruc, address, phone)
VALUES ('904fad55-e273-4533-aa73-4bb4daa4171e', 'Ventatec', NULL, NULL, NULL)
ON CONFLICT (id) DO NOTHING;

INSERT INTO users (id, company_id, email, role, first_name, last_name, status, password_hash)
VALUES (
  '7b03222e-c5b3-4912-9565-95dda72222f8',
  '904fad55-e273-4533-aa73-4bb4daa4171e',
  'livan.05@hotmail.com',
  'admin',
  'Livan',
  'Tunon',
  'active',
  '$2a$10$d8UdlIbA4nESHywDqYk6h.L2W6qk9YEUiPqNnUm5qgQmKKGAwk9Q.'
)
ON CONFLICT (id) DO NOTHING;
