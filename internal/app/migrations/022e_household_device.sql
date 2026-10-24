-- The Pi's own scanner is a direct device on household 1. It uses no HTTP auth.

INSERT INTO devices (id, household_id, kind, label, created_at)
VALUES ('{{device}}', '{{household}}', 'direct', 'This server''s scanner', '{{issued}}');

INSERT INTO change_versions (household_id, scope, version) VALUES
('{{household}}', 'scans', 0),
('{{household}}', 'stock', 0),
('{{household}}', 'list', 0),
('{{household}}', 'devices', 0);
