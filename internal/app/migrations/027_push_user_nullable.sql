-- A household-password or LAN subscription has no account, so user_id is NULL.
-- One browser endpoint can subscribe in two households: the key is
-- (household_id, endpoint). Blank account ids from earlier builds become NULL
-- so retirement can delete them with user_id IS NULL.

CREATE TABLE push_subscriptions_user (
    household_id TEXT NOT NULL DEFAULT '{{household}}',
    endpoint     TEXT NOT NULL,
    p256dh       TEXT NOT NULL,
    auth         TEXT NOT NULL,
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    origin       TEXT NOT NULL DEFAULT '',
    user_id      TEXT,
    device_id    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (household_id, endpoint)
);

INSERT INTO push_subscriptions_user (
    household_id, endpoint, p256dh, auth, created_at, updated_at, origin, user_id, device_id
)
SELECT household_id, endpoint, p256dh, auth, created_at, updated_at, origin,
       NULLIF(user_id, ''), device_id
FROM push_subscriptions;

DROP TABLE push_subscriptions;
ALTER TABLE push_subscriptions_user RENAME TO push_subscriptions;
