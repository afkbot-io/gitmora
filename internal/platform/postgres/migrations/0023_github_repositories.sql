-- Existing physical identity columns retain their checksums/legacy compatibility.
-- The explicit discriminator prevents interpreting GitHub IDs or credentials as GitLab.
ALTER TABLE repositories ADD COLUMN provider text NOT NULL DEFAULT '' CHECK (provider IN ('', 'github'));

ALTER TABLE forge_connections ADD COLUMN anonymous boolean NOT NULL DEFAULT false;
