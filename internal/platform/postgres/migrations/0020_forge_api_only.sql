-- Preserve every existing policy and lease. API-only is enabled only by a verified replacement.
ALTER TABLE forge_connections DROP CONSTRAINT forge_connections_webhook_mode_check;
ALTER TABLE forge_connections ADD CONSTRAINT forge_connections_webhook_mode_check
 CHECK(webhook_mode IN('signature','legacy','disabled'));
