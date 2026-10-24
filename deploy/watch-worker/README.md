# pantry-pi-watch

Cloudflare Worker that notices when the Pi is unreachable or a backup in R2 is missing, stale, or too small. The Pi cannot report its own death. This Worker runs outside the Pi, on Cloudflare's cron.

Alerts go out through Cloudflare Email Service (`send_email`) to `rhionin@gmail.com`. healthchecks.io is not required. CJ's decision on 2026-10-23 was to send the email from the Worker instead of standing up a healthchecks.io account. A Cloudflare Tunnel Health Alert for tunnel `pantry-pi` is the other outside signal. That alert is a dashboard notification, not this Worker.

The Worker has no public route and `workers_dev` is false. Nothing on the internet can call it. If the Worker itself stops running, it cannot email about that. The Tunnel Health Alert still covers a dead tunnel. The optional healthchecks URLs below are the dead-man switch for the Worker, and they are unset unless you add them.

## What it checks

Cron `*/5 * * * *`:

- **Uptime, every run.** `GET ${TARGET_URL}/api/build` must return HTTP 200 and a JSON body within 10 seconds. Redirects count as failure.
- **Backups, when the UTC minute is 0** (and on a manual `/run` during `wrangler dev`). Lists `nightly/` and `manual/` in the `pantry-pi-backups` bucket.
  - The newest `*.pantry.db.gz.age` across those prefixes (a manual snapshot counts when it is the newest, including when `nightly/` does not exist yet) must be 26 hours old or newer. Age uses the R2 upload time.
  - Its `.manifest.json` must say `integrity` is `ok`, and `ciphertext_size` must equal the R2 object size.
  - When an older snapshot exists in the same prefix, the newest object must be at least half that size.
  - No object in `nightly/` or `manual/` may be older than 15 days. An object exactly 15 days old is still inside the window.

`_watch/state.json` is this Worker's own dedup record. It is not a backup and it is not part of the 15-day sweep. `secrets/` is not part of this sweep.

## Alerts

State is a small JSON object at `_watch/state.json` in the same bucket. Uptime and backup are tracked separately.

- `ok` → `failing` sends one `[ALERT]` email.
- Further failures stay quiet for 6 hours, then send one `[ALERT]` reminder. They do not email every 5 minutes.
- `failing` → `ok` sends one `[RESOLVED]` email.

Emails contain only the check name, status, HTTP status, object keys, sizes, and timestamps. Each timestamp is America/Denver (MDT or MST) and UTC. They do not contain names, barcodes, row contents, or manifest table counts.

`ALERT_TO` is `rhionin@gmail.com`, which is also the binding's `destination_address`. `ALERT_FROM` is `pantry-pi-watch@rhionin.com`.

Optional secrets `HC_UPTIME_URL` and `HC_BACKUP_URL`, unset by default, are pinged on success. A failure pings the same URL with `/fail` appended. Leave them unset to rely on the email binding.

Optional secret `BACKUP_DRILL_PREFIX` lists that prefix instead of `nightly/` and `manual/`. Unset, it does nothing. See the drill below.

## Deploy

The bucket `pantry-pi-backups` must already exist. This Worker does not create it. The binding reads `nightly/` and `manual/` and writes only `_watch/state.json`.

Before the first deploy:

- Email Routing has `rhionin@gmail.com` as a verified destination.
- `rhionin.com` is onboarded for sending, so `pantry-pi-watch@rhionin.com` is an allowed From address.

From this directory:

```bash
npm ci
npx wrangler deploy
```

Wrangler uses the account you are logged in as. Do not put API tokens in the command line. Confirm the cron trigger in the dashboard after deploy.

To opt into the healthchecks hook later:

```bash
npx wrangler secret put HC_UPTIME_URL
npx wrangler secret put HC_BACKUP_URL
```

Paste the success URL only, with no `/fail` suffix. `npx wrangler secret delete HC_UPTIME_URL` removes one. A secret put or delete deploys a new version.

## Tests

```bash
npm test
```

Node.js 22 or newer. The tests use Vitest and a fake bucket. They do not call Cloudflare and they do not send mail.

## Alert drill

Record the times in MDT on issue #382.

**Uptime.** Needs an explicit go-ahead. In a quiet hour, stop the pantry container for 15 minutes (`docker compose stop pantry`). Leave cloudflared running. One `[ALERT] pantry-pi-watch uptime` email should arrive within about 10 minutes. More 5-minute failures stay quiet. The Tunnel Health Alert should not fire, because the tunnel stays up. Start pantry again. One `[RESOLVED] pantry-pi-watch uptime` email follows the next successful check.

**Backup.** Point the hourly check at an empty prefix:

```bash
npx wrangler secret put BACKUP_DRILL_PREFIX
```

Enter `drill-empty/`. The next run whose UTC minute is 0 sends one `[ALERT] pantry-pi-watch backup` email. Then:

```bash
npx wrangler secret delete BACKUP_DRILL_PREFIX
```

The next hour sends `[RESOLVED] pantry-pi-watch backup`.

`npx wrangler dev` and `curl http://127.0.0.1:8787/run` run both checks against the local simulated bucket and print the MIME message instead of emailing. Do not add `--remote` for a casual run: that uses the real bucket and can send a real email.

## Local development

```bash
npm ci
npx wrangler dev
```

`GET` or `POST /run` runs uptime and backups immediately. Any other path returns 404. There is still no hostname once the Worker is deployed.
