-- Invites remember the role chosen when they were created.
-- Stage 1 does not store an email address on the invite.

ALTER TABLE invites ADD COLUMN role TEXT NOT NULL DEFAULT 'member'
    CHECK (role IN ('owner', 'member'));
