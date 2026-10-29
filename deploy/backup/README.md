# Pantry database backup

The nightly job uploads an age-encrypted snapshot of `pantry.db` to an
R2 bucket. The backup binary is `stockbot-backup` in the pantry image,
next to `stockbot-server`. There is no second image.

## Where it runs

The timer and the install script run on the Pi. They share the updater
lock at `/opt/pantry/deploy-trigger/.lock`. Before the updater recreates
the pantry container for a new image, it runs this script with
`pre-deploy` while it still holds that lock. It sets
`PANTRY_BACKUP_LOCK_HELD=1` so this script does not flock the same file
again. A backup that is not started from the updater takes the lock itself.

`BACKUP_BEFORE_DEPLOY` controls that pre-deploy run. `required` (the default)
leaves the running containers in place, records `last-deploy-status`, and
the updater exits non-zero when the backup fails. `warn` and `optional`
log a failure and the deploy continues. `off` skips the backup. Set it in
the environment or in `/opt/pantry/.env`. A minute that does not change
the pantry image does not take a backup.

The restore drill runs only on CJ's own computer. It does not run on the
Pi, a shared machine, or an agent. It never reads the live database.

## Staging

The snapshot is written to `/run/stockbot-backup` (a 64MiB tmpfs; `/run`
is tmpfs on the Pi) when the database, plus headroom (two and a half
times the main file and WAL), fits. Otherwise it is written to
`/data/.backup-staging` on the pantry data volume. The plaintext file is
removed once the upload's size and sha256 match. If that check fails, or
the container is killed, the EXIT trap in `deploy/systemd/pantry-backup.sh`
removes the staging directory. The next run also removes anything left
there. The log line is `backup staging: tmpfs ...` or
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
The cutoff uses the backup server's clock (the LastModified time of the
object just uploaded), not the Pi clock. The Pi has no battery-backed
clock. If that clock is more than 12 hours away from the server, the
sweep deletes nothing and the job exits 30. The sweep also never deletes
the object just uploaded or the newest database backup.

If an object older than 15 days remains, or a delete fails, the job
exits 30. A database object counts as a backup only when its manifest
was stored too. Once the database object and its manifest are both verified, a later
failure leaves them in the bucket. That includes the secrets archive,
the sweep, a clock that is too far from R2, a delete that did not
succeed, the stale check, and prune. The job still sends a failure ping
and exits non-zero. A failure before that verification deletes the
objects from that run, so an incomplete upload does not count as a fresh
backup. `stockbot-backup stale` uses the backup server's clock. It
exits non-zero when the newest manifest-backed database backup is older
than 26 hours there. A backup that is 25 hours old stays fresh when the
Pi clock is 2 hours fast. An object
exactly 14 days old is kept; one a second older is deleted. The same
strict instant applies at 15 days and at 26 hours.

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

The timer is `OnCalendar=*-*-* 03:30:00 America/Denver`, with a 10 minute
random delay. That form stays on Denver time across the November DST
change. Re-running setup does not enable the timer on a machine that has
never had it installed.

If the backup cannot take the deploy lock, it exits 30 and posts a
failure to the healthchecks URL. The URL is not printed.

## R2 bucket

Create the bucket on CJ's Cloudflare account. The default name is
`pantry-pi-backups`. Do not enable replication and do not add a second
bucket.

Add an R2 lifecycle rule that expires objects 14 days after they are
uploaded. R2 removes expired objects on its own, usually within a day.
The nightly sweep above is the second control, for when that rule is slow.

Create the R2 API token so it can reach only this bucket. It is not an
account-wide token. The install script writes it into the mode `0400`
settings file and does not put it in the environment.

## Secrets archive

The library leaves this off unless `--with-secrets` or
`BACKUP_INCLUDE_SECRETS=1` is set, with paths from `--secrets-path` or
`BACKUP_SECRETS_PATHS` (comma-separated). An empty list skips the archive
and the database backup still succeeds. A missing path or a symlink fails
that run.

On the Pi, the nightly wrapper archives these regular files when they
exist: a legacy `/opt/pantry/auth.caddy` if one is still on disk, and `.env` backups named `.env.bak`,
`.env.bak.*`, `.env.backup`, or `.env.backup.*`. The live
`/opt/pantry/.env` is not in that list. To add paths, including the live
`.env` if you want it, set `PANTRY_BACKUP_SECRETS_PATHS` to a
comma-separated list of absolute paths when you run the installer. It
writes only those paths to `/opt/pantry/backup/secrets-paths`. Nothing
else is added. The wrapper copies the chosen files onto a tmpfs as mode
`0400` for uid 65532, mounts that directory read-only, and shreds the
copy when the script exits. `.env.example` and the R2 settings file are
not included. A symlink, or a configured path that is not a regular
file, sends a healthchecks failure ping and exits before the backup
container starts. A missing legacy `auth.caddy` is normal and does not fail the run. The archive uses the
same age public key and the same 14-day retention, under `secrets/`.

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

`--identity` passes a private-key file that stays on this computer.
`--paste-identity` reads the key from the terminal into the scratch
directory (on `/dev/shm` when that is writable) and deletes that copy
when the drill ends. An identity file outside the scratch directory is
not deleted. `AGE_IDENTITY`, `AGE_IDENTITY_FILE`, `BACKUP_AGE_IDENTITY`,
`BACKUP_IDENTITY`, `SOPS_AGE_KEY`, `SOPS_AGE_KEY_FILE`, and any
environment value containing `AGE-SECRET-KEY-` are refused. Only a
variable name that is a shell identifier is printed. The value is not.

The restored app runs on an internal Docker network, with no published
port and no route to the internet. A second container on that same
network, not on the default bridge and not via localhost, requests
`GET /api/build` and `GET /api/products`. The script refuses to start
when `/opt/pantry/docker-compose.yml` is present, so it does not run on
the Pi.

Pull the probe image on this computer before the drill. The script does
not pull it:

```bash
docker pull curlimages/curl@sha256:c1fe1679c34d9784c1b0d1e5f62ac0a79fca01fb6377cdd33e90473c6f9f9a69
```

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
