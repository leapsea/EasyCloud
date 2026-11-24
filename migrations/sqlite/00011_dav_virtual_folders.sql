-- +goose Up
-- Virtual folders: rows with a folder name are entries of the virtual folder
-- of that name at the endpoint's root (read-only), instead of shared
-- directories of their own. '' = a shared directory, as before.
ALTER TABLE easycloud_dav_dirs ADD COLUMN folder TEXT NOT NULL DEFAULT '';
-- Version 10 is unused: a discarded design (channels) briefly shipped it in
-- development builds; drop what it may have created.
DROP TABLE IF EXISTS easycloud_dav_config_channels;
DROP TABLE IF EXISTS easycloud_dav_channel_items;
DROP TABLE IF EXISTS easycloud_dav_channels;

-- +goose Down
ALTER TABLE easycloud_dav_dirs DROP COLUMN folder;
