# Pantry database backup

The nightly job uploads an age-encrypted snapshot of `pantry.db` to an
R2 bucket. The backup binary is `stockbot-backup` in the pantry image,
next to `stockbot-server`. There is no second image.

## Where it runs

The timer and the install script run on the Pi. They share the updater
lock at `/opt/pantry/deploy-trigger/.lock`. The updater's own lock
behavior is unchanged.

The restore drill runs only on CJ's own computer. It does not run on the
Pi, a shared machine, or an agent. It never reads the live database.

## Staging

The snapshot is written to the container's `/stage` tmpfs when the
database, plus headroom (two and a half times the main file and WAL),
fits in the 64MiB tmpfs. Otherwise it is written to
`/data/.backup-staging` on the pantry data volume. That directory is
removed as soon as the ciphertext exists, again when the process
returns, and by an EXIT trap in `deploy/systemd/pantry-backup.sh` if the
container is killed. The log line is `backup staging: tmpfs ...` or
`backup staging: volume ...`. A backup is not refused because of size.

## What is stored

Object keys use UTC:

- `nightly/<timestamp>.pantry.db.gz.age` and `.manifest.json`
- `manual/<timestamp>.<reason>.pantry.db.gz.age` for `manual`, `pre-deploy`, or `drill`
- `secrets/<timestamp>.tar.age` only when secrets archiving is enabled

The manifest records counts, ids, hashes, sizes, the migration id, the
tool version, the image digest, the duration, and the staging kind. It
does not record household names, barcodes, or row contents.

Nightly, manual, and secrets objects older than 14 days are deleted.
If an object older than 15 days remains, or a delete fails, the job
exits 30. `stockbot-backup stale` exits non-zero when the newest
database backup is older than about 26 hours.

## Credentials

R2 settings are read from a mode `0400` file mounted into the container.
They are not read from environment variables and they are not logged.
The age *public* key is a separate file. A private key (`AGE-SECRET-KEY-`)
is rejected.

`deploy/backup/install.sh` prompts for those values (`read -rs` for the
secret access key) and writes the files. Run it on the Pi as root:

```bash
sudo bash deploy/backup/install.sh
```

The timer is `03:30` America/Denver, with a 10 minute random delay.
Re-running setup does not enable the timer on a machine that has never
had it installed.

## Secrets archive

Off by default. Turn it on with `--with-secrets` or
`BACKUP_INCLUDE_SECRETS=1` in the settings file, and list paths with
`--secrets-path` or `BACKUP_SECRETS_PATHS` (comma-separated). The paths
are tarred, age-encrypted to the same public key, and uploaded under
`secrets/` with the same 14-day retention. The Pi paths are not
hard-coded; an empty list skips the archive and the backup still
succeeds. A missing path or a symlink fails the run.

## Pre-migration copies

`--prune-premigration` deletes `pantry-pre-*.db` next to the database.
It is off unless that flag is passed. The nightly job does not delete
those files on its own.

## Restore drill

On CJ's computer, after downloading an object (or pointing at a local
directory of objects):

```bash
bash deploy/backup/restore-drill.sh \
  --identity /path/to/age-identity.txt \
  --file /path/to/snapshot.pantry.db.gz.age \
  --manifest-file /path/to/snapshot.manifest.json
```

`--identity` is the only way to pass the private key. `AGE_IDENTITY`,
`AGE_IDENTITY_FILE`, `BACKUP_AGE_IDENTITY`, `BACKUP_IDENTITY`,
`SOPS_AGE_KEY`, `SOPS_AGE_KEY_FILE`, and any environment value containing
`AGE-SECRET-KEY-` are refused. The variable name is printed; the value
is not.

The script checks the plaintext sha256, runs the integrity check,
compares row counts with the manifest, starts the image against the
copy with no internet, requests `GET /api/build` and `GET /api/products`,
prints the elapsed time, and deletes its scratch directory. It does not
delete an identity file that lives outside that directory. `age` must
be installed on this computer. `stockbot-backup` on `PATH` is used for
the verify step when it is present.

`--secrets` also decrypts the newest `secrets/*.tar.age` in
`--bucket-dir`, checks the manifest file count, and deletes the
plaintext. Leave it off when there is no secrets archive.

## Disaster restore

Restoring onto the Pi is a separate manual step. It is not what the
drill does, and it needs an explicit go-ahead. Stop the pantry
container, copy the verified database to `/data/pantry.db` as uid
65532, remove any `-wal`, `-shm`, and `-journal` files beside it, start
the stack, and check `GET /api/build`.
