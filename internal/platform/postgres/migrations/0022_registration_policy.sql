-- Signup policy is independent from login/session configuration. Disabling signup never logs out the team.
ALTER TABLE identity_runtime ADD COLUMN registration_enabled boolean NOT NULL DEFAULT false;
