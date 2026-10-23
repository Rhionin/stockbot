# Stockbot Deployment Guide

This guide covers deploying Stockbot on a Raspberry Pi using Docker Compose.

The public site is `https://stockbot.rhionin.com`. The previous name `pantry.rhionin.com` redirects there while `LEGACY_PUBLIC_HOST` is set. See [Previous public hostname](#previous-public-hostname).

## Names that stay pantry

The Go module is `github.com/Rhionin/stockbot`. The Pi keeps the names from the first install. Changing them would point at a different directory, image, volume, or Grocy record.

- LAN name `pantry.local` and Avahi file `pantry-http.service`. The on-screen mDNS label is Stockbot.
- Directory `/opt/pantry`. That directory name is the Compose project. Grocy on this Pi is `/home/pantrybot/grocy`.
- Images `ghcr.io/rhionin/pantry` and `ghcr.io/rhionin/pantry-grocy-sync`. Containers `pantry`, `pantry-caddy`, `pantry-cloudflared`, and `pantry-grocy-sync`. Volume `pantry-data`. Database `/data/pantry.db`.
- Compose service `pantry`. The sidecar calls it at `http://pantry:8080`. Docker network `pantry_cf-tunnel`.
- Scanner node `/dev/input/pantry-scanner`, udev rule `99-pantry-scanner.rules`, and units `pantry-update.service`, `pantry-update.path`, `pantry-update.timer`, `pantry-lan-only.service`, and `pantry-mdns.service`.
- Headers `X-Pantry-Deploy-Timestamp`, `X-Pantry-Deploy-Signature`, `X-Pantry-Entry`, and `X-Pantry-Client-IP`. The Caddy basic-auth realm stays `Pantry`. The shared login username stays `pantry`.
- Environment variables and Actions secrets that start with `PANTRY_`.
- Browser keys `pantry-scan-alerts`, `pantry-scan-alert-open`, and `pantry-scan-alert-direction`, and notification tags that start with `pantry-scan-`.
- Grocy location and shopping list named `Pantry`, the location description `Synced from Pantry`, and userfields `pantry_id`, `pantry_name`, `pantry_size`, and `pantry_rule`.
- Inventory type `Pantry` (`NewPantry`).

## Quick Start

New to this? Follow these steps in order on your Raspberry Pi and you'll have Stockbot running. The setup script does the heavy lifting; each step says what it does. For options, backups, and troubleshooting, use the reference sections further down.

1. **What you need.** A Raspberry Pi running 64-bit Raspberry Pi OS, and the ability to open a terminal. (See [Supported Platform](#supported-platform) below for exact hardware and OS details.)

2. **Get the Stockbot files onto the Pi.** The deployment files live in this repository's `deploy/` folder: `docker-compose.yml`, `Caddyfile`, `.env.example`, `setup.sh`, and the `systemd/` and `udev/` folders. Copy that whole folder onto your Pi (for example, clone the repo with `git`, or copy with a USB drive or `scp`).

3. **Run the setup script.** From the `deploy/` folder you copied over:

   ```bash
   sudo ./setup.sh
   ```

   This installs Docker if needed, copies the deployment files into `/opt/pantry`, creates your configuration, installs the scanner udev rule, starts Stockbot, and waits until it reports healthy. It is safe to re-run: it never overwrites configuration values you already set. When `PUBLIC_HOST` is already set and `/opt/pantry/auth.caddy` exists, it also keeps the public HTTPS proxy on the current compose file and Caddyfile, and it applies the LAN firewall on the published Stockbot port. `install`, `publish`, and `firewall` are the same command.

4. **Check it works.** The installer already polled `/health` for you, but you can confirm any time. This should print `{"status":"ok"}`:

   ```bash
   curl http://localhost:8080/health
   ```

   You can also open `http://<pi-ip-address>:8080` in a web browser on any device on the same network. After setup, `sudo ./setup.sh status` prints that address. When Avahi is installed it also publishes `http://pantry.local:8080`. If `https://stockbot.rhionin.com` hangs on home Wi-Fi and loads on cellular, the router is not hairpinning; use the LAN address. See [Home Wi-Fi hangs on the public name](#home-wi-fi-hangs-on-the-public-name).

5. **See the full picture.** Run the diagnostic any time to see the whole chain from udev rule to scan ingestion:

   ```bash
   sudo ./setup.sh status
   ```

6. **Optional: open it to the public internet.** The steps above stay on your home network. To serve the same UI at a hostname you own, such as `https://stockbot.rhionin.com`, follow [Public Internet access](#public-internet-access). The Gryphon router does not hairpin, so that name hangs on home Wi-Fi when it is reached through a port forward. [Cloudflare Tunnel](#cloudflare-tunnel) is the path that works on home Wi-Fi and on cellular without forwarding ports. The public site shows a login page for the one shared password. The timing snapshot at `/api/telemetry` and the running build at `/api/build` are public on that hostname.

## Updating

After `git pull` on the Pi, from `deploy/`:

```bash
sudo ./setup.sh
```

That one command is the whole update. It copies `deploy/` to `/opt/pantry`, pulls the Stockbot image when the public site is on, starts the containers from the compose file you just pulled (including the public HTTPS proxy when `PUBLIC_HOST` is set and `/opt/pantry/auth.caddy` is already there), restarts Caddy so the current `Caddyfile` is what is serving, applies the LAN firewall for the published Stockbot port, and republishes `pantry.local` when Avahi is installed. When `CLOUDFLARE_TUNNEL_TOKEN` is set, the same command keeps the tunnel profile instead of ports 80 and 443. The public hostname shows the in-app login page. These paths stay reachable with no login: `GET /api/telemetry`, `POST /api/telemetry/client`, `GET /api/build`, `GET /brand/logo.png`, `GET /terms`, `GET /privacy`, `POST /api/deploy-hook`, and `POST /api/sentry-tunnel`. Run it again any time. Existing `.env` values stay, an existing `auth.caddy` is not regenerated, and the session secret in `.env` is not replaced, so phones that are already signed in stay signed in. A site that is already on the public internet stays there. LAN `http://<pi-ip>:8080` stays up and still does not ask for a password. On the certificate path, the public name still hangs on home Wi-Fi until the router hairpins or you use the LAN address; see [Home Wi-Fi hangs on the public name](#home-wi-fi-hangs-on-the-public-name). Tunnel mode does not have that hang.

`sudo ./setup.sh firewall-off` removes the port rule until the next setup. To leave it off, set `PANTRY_LAN_FIREWALL=off` in `/opt/pantry/.env` and run `sudo ./setup.sh` again.

**The scanner is optional at every step.** Stockbot starts and serves the web UI whether or not a barcode scanner is attached, and you can connect or disconnect the scanner at any time — see [Headless Scanner Input](#headless-scanner-input).

The rest of this guide is reference material: configuration options, backups, the barcode scanner, rolling back, and troubleshooting.

## Supported Platform

**Target:** 64-bit Raspberry Pi OS on arm64 hardware

Stockbot is packaged as a `linux/arm64` container image and requires 64-bit Raspberry Pi OS running on compatible hardware (Raspberry Pi 3B+, 4, 5, or newer models with arm64 architecture).

## First-Time Setup

The recommended path is `sudo ./setup.sh`, which performs every step below in order and verifies the result. The steps are documented here so you understand what the script does and can run them by hand if you prefer.

### 1. Install Docker and Docker Compose

Install Docker and the Compose plugin on your Raspberry Pi:

```bash
# Update system packages
sudo apt update && sudo apt upgrade -y

# Install Docker (this is what setup.sh runs)
curl -fsSL https://get.docker.com | sh

# Add your user to the docker group (requires logout/login to take effect)
sudo usermod -aG docker $USER

# Verify installation
docker --version
docker compose version
```

Log out and back in for the group membership to take effect.

### 2. Set up deployment files and configuration

`sudo ./setup.sh` copies the whole `deploy/` tree to `/opt/pantry` (including `systemd/` and `udev/`) and reconciles `.env`. To do it by hand:

```bash
# Create deployment directory
sudo mkdir -p /opt/pantry

# Copy the entire deploy/ directory contents to /opt/pantry
# (docker-compose.yml, Caddyfile, .env.example, setup.sh, systemd/, udev/)

# Create your config from the example; the defaults work to get started
cd /opt/pantry
sudo cp .env.example .env
sudo nano .env  # edit if needed — see the Configuration table below
```

The script's `.env` handling is idempotent: it creates `.env` from `.env.example` only when it is absent, and on later runs appends only keys that are missing, never touching a value you already set. A pinned `PANTRY_IMAGE_TAG` therefore survives re-running `install`.

### 3. Install the scanner udev rule

```bash
sudo ./setup.sh rule
```

This detects your scanner and writes `/etc/udev/rules.d/99-pantry-scanner.rules` with a symlink at `/dev/input/pantry-scanner`. The generated match uses the input parent's vendor and product IDs, which covers USB HID and Bluetooth HID. See [Headless Scanner Input](#headless-scanner-input) to write the rule by hand. This step is optional — Stockbot runs without it — but scanning won't work until a matching rule exists.

### 4. Start Stockbot

```bash
cd /opt/pantry
sudo docker compose up -d
```

The container starts whether or not the scanner is attached. There is **no ordering requirement**: you can install the udev rule and attach the scanner before or after starting the container, in any order.

### 5. Verify deployment

```bash
# Health check — should return {"status":"ok"}
curl http://localhost:8080/health

# Full chain diagnostic
sudo ./setup.sh status

# Access the web UI at http://<pi-ip-address>:8080 from any device on your network
```

## Public Internet access

This puts the Stockbot UI on a hostname you already own, with HTTPS, using the same Docker Compose stack. The recommended name is a subdomain (`stockbot.rhionin.com`) so the bare domain can stay unused.

The Gryphon in this house does not hairpin, so `https://stockbot.rhionin.com` hangs on home Wi-Fi when DNS points at the router's WAN address. [Cloudflare Tunnel](#cloudflare-tunnel) is how that URL works on home Wi-Fi and on cellular without forwarding ports 80 and 443. The steps in this section are the certificate and port-forward path. Leave those forwards in place until the tunnel has been tested on both networks.

The public site asks for one shared password before it serves the UI, the API, and the live scan stream. That stops scanners and other bots that do not have the password. It is not separate accounts, and anyone who has the password can change the pantry. These exact paths stay open without that password: `GET /api/telemetry`, `POST /api/telemetry/client`, `GET /api/build`, `GET /brand/logo.png`, `GET /terms`, `GET /privacy`, `POST /api/deploy-hook`, and `POST /api/sentry-tunnel`. The snapshot is counters and durations only — no barcodes, product names, or user ids — so an agent can `curl` it. `GET /api/build` is the running commit, build time, and version, also with no secrets. The brand mark and the two legal pages are public because a grocery login stores those URLs. The deploy hook does not use the household password; it rejects any request whose HMAC signature or timestamp does not check out. See [Deploy when the image is published](#deploy-when-the-image-is-published). The home-network address `http://<pi-ip>:8080` does not ask for the password, so do not forward port 8080 on the router.

### Which build is running

`GET /api/build` needs no password on the public hostname. Compare `commit` with the `master` SHA to confirm a deploy landed. A local binary returns `commit` `unknown` and `version` `dev`, and omits the other fields.

| Field | Meaning |
| --- | --- |
| `commit` | Full git SHA, or `unknown` when the binary was not stamped |
| `committedAt` | Commit time, ISO 8601. Omitted when unset |
| `builtAt` | Image build time, ISO 8601 UTC. Omitted on a local build |
| `version` | `dev` locally. CI stamps `v<UTC yyyymmdd>-<7 character sha>`, the same formula as the annotated release tag |
| `subject` | Commit subject. Omitted when unset |

```bash
curl -fsS https://stockbot.rhionin.com/api/build
```

The menu's Build section shows the same version and commit. The document has no environment values, hostnames, or paths.

The path below is Caddy in the `public` Compose profile, a Let's Encrypt certificate, and the Dynu A record for `stockbot.rhionin.com`. It needs a public IPv4 address and the ability to forward TCP ports 80 and 443. If your ISP uses CGNAT, skip to [When port forwarding cannot work](#when-port-forwarding-cannot-work). With `CLOUDFLARE_TUNNEL_TOKEN` empty, these steps are unchanged, and `setup.sh` does not call Dynu or stop its update client.

### 1. Confirm the Pi is reachable from the internet

On the Pi:

```bash
curl -4 https://ifconfig.me
```

That prints the address the internet sees. On your router, open the WAN / internet status page and read the WAN IP.

- If those two addresses match, and the WAN address is not in `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, or `100.64.0.0/10`, port forwarding can work.
- If they differ, or the router WAN address is in one of those ranges, the ISP is using CGNAT. Forwarding ports will not make the Pi reachable. Use [Cloudflare Tunnel](#when-port-forwarding-cannot-work) instead.

Reserve a DHCP lease for the Pi (or set a static LAN address) so the forward does not follow the Pi to a new address later.

### 2. Forward ports on the router

Create two forwards to the Pi's LAN address:

| WAN (external) | Pi (internal) |
|----------------|---------------|
| TCP 80 | TCP 80 |
| TCP 443 | TCP 443 |

UDP 443 is optional. It enables HTTP/3. The site works with only the two TCP forwards.

Do not forward port 8080. Leave UPnP off, and delete any other forward you are not using.

The published Stockbot port is IPv4-only (`0.0.0.0`). That keeps a global IPv6 address on the Pi from answering on 8080. It does not stop a router forward: a forwarded packet is still addressed to the Pi's LAN IPv4 address, and Docker's userland proxy accepts it. `ufw` does not close that port either. Docker accepts the connection in its own proxy and, separately, DNATs it around the `INPUT` chain `ufw` edits.

If `sudo ufw status` says `active`, allow the proxy ports. Leave a firewall that is currently inactive turned off. Enabling it without an SSH allow rule can lock you out of the Pi.

```bash
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 443/udp
```

`sudo ./setup.sh` also drops non-LAN clients that reach the Stockbot port (loopback, private LAN, and Tailscale `100.64.0.0/10` stay allowed; ports 80 and 443 are not changed). That runs whenever compose publishes the port, which the stock file always does, and whenever the public proxy is on. Set `PANTRY_LAN_FIREWALL=off` in `/opt/pantry/.env` and run `sudo ./setup.sh` again to leave the port reachable from any source. `sudo ./setup.sh firewall-off` removes the rule only until the next setup. Do not forward port 8080 either way. The router rule above is the one that matters for the public site.

### 3. Point stockbot.rhionin.com at this Pi

DNS for `rhionin.com` is at [Dynu](https://www.dynu.com/) today (dynamic DNS). While Dynu's nameservers are in place, the record phones look up is the one in Dynu. A custom record at Squarespace is not authoritative. `sudo ./setup.sh` does not call Dynu and does not stop a Dynu update client. With `CLOUDFLARE_TUNNEL_TOKEN` empty, leave that client running so `stockbot.rhionin.com` keeps tracking the WAN address.

In the Dynu panel, confirm `stockbot` is an A record, or a Dynu dynamic hostname, for the IPv4 from `curl -4 https://ifconfig.me` on the Pi. Then:

```bash
dig +short stockbot.rhionin.com A
```

The answer must be the same address `curl -4 https://ifconfig.me` prints. If `dig` is not installed, `getent hosts stockbot.rhionin.com` is enough. When the home IP changes, the Dynu client updates this record. Do not turn the client off while the tunnel token is empty. Tunnel mode stops using the A record only after the Cloudflare hostname route is saved and the token is set. Until then, cellular still needs Dynu.

If the nameservers point at Squarespace instead of Dynu, add the record there. That is not this house's current DNS. Registration can stay at Squarespace either way.

1. Open the [Squarespace domains dashboard](https://account.squarespace.com/domains) and sign in.
2. Click **rhionin.com**.
3. Click **DNS**, then **DNS Settings**.
4. Under **Custom records**, click **Add record**.
5. Set:
   - **Type:** A
   - **Host:** `stockbot`  
     Squarespace appends `.rhionin.com`. Do not type the full name, and do not type `https://`.
   - **Data** (IP address): the IPv4 from `curl -4 https://ifconfig.me`
6. Save the record.

Leave the Squarespace default records for `@` and `www` alone. A new host named `stockbot` does not conflict with them. Use host `@` only if you want `https://rhionin.com` itself; in that case delete the Squarespace default A and CNAME records for `@` first, because a custom record cannot override those presets. Squarespace does not offer a TTL field. Those steps do nothing for resolution while Dynu is authoritative.

When your home IP changes and Dynu is still authoritative, the Dynu update client changes the A record. The certificate is for the hostname, so HTTPS starts working again as soon as DNS matches the new address. Leave that client running whenever `CLOUDFLARE_TUNNEL_TOKEN` is empty. Squarespace itself has no dynamic-DNS service; a Squarespace A record would be a manual edit, and only if Squarespace is actually the DNS host.

### 4. Start the proxy

On the Pi, edit `/opt/pantry/.env` (create the LAN install first with `sudo ./setup.sh` if you have not):

```bash
PUBLIC_HOST=stockbot.rhionin.com
ACME_EMAIL=you@example.com
BASIC_AUTH_USER=pantry
BASIC_AUTH_PASSWORD=replace-with-a-long-passphrase
```

`PUBLIC_HOST` is the hostname only. `ACME_EMAIL` is where Let's Encrypt sends expiry notices. Replace `BASIC_AUTH_PASSWORD` with a passphrase of 12 to 72 characters, and do not wrap it in quotes. The first `sudo ./setup.sh` after that hashes it into `/opt/pantry/auth.caddy` (mode `0600`) and restricts `.env` to its owner. The password itself stays in `.env`. It is not written into the image or the repository. A later run keeps the existing `auth.caddy` and does not ask for the password again. Setup copies that hash to `/opt/pantry/auth/household` for the login page and, the first time, adds `PANTRY_SESSION_SECRET` to `.env`. Later runs keep both. The `auth` directory is mode `0700` and the hash file is mode `0600`, both owned by uid 65532, the distroless `nonroot` user in the image. A root-owned file cannot be read by the container, and the public site then refuses to sign in. You do not pick a new password for the login page.

```bash
sudo ./setup.sh
```

That refreshes `docker-compose.yml` and `Caddyfile` into `/opt/pantry`, starts Stockbot, and starts [Caddy](https://caddyserver.com/) on ports 80 and 443. Caddy requests a Let's Encrypt certificate for `PUBLIC_HOST` and renews it on its own. Starting the proxy accepts the Let's Encrypt subscriber agreement. The same command restarts Caddy after a later pull so a new `Caddyfile` is what is serving.

Certificates are stored in the Docker volume `caddy-data`. Do not delete that volume to "retry" a failure: Let's Encrypt rate-limits repeat issuances (on the order of five duplicate certificates per hostname per week).

### 5. Prove it is public

From a phone on cellular data, not the home Wi-Fi:

```bash
curl -fsS -u 'pantry:replace-with-a-long-passphrase' https://stockbot.rhionin.com/health
```

Without the password, that command returns `401` JSON and does not open a browser password prompt. With the `Authorization: Basic` header, expect `{"status":"ok",...}`. Then open `https://stockbot.rhionin.com` in the phone's browser. The login page asks for the same username and password. After that, the pantry UI loads. The session cookie is HttpOnly and SameSite=Lax, Secure on the public site, and lasts 180 days, so the phone stays signed in. Log out is in the header menu. The scan queue and inventory pages keep a live connection to `/api/events`; new scans should show up without a refresh.

The timing snapshot does not use the password. From the same phone:

```bash
curl -fsS https://stockbot.rhionin.com/api/telemetry
```

Expect JSON with a `pageLoad` object. A `401` on `/health` without a password is still correct. Caddy also serves these exact paths without the password:

```bash
curl -fsS -D - -o /dev/null https://stockbot.rhionin.com/brand/logo.png
curl -fsS -D - -o /dev/null https://stockbot.rhionin.com/terms
curl -fsS -D - -o /dev/null https://stockbot.rhionin.com/privacy
```

Expect `200` with `image/png` for the mark and `text/html` for the two pages. `GET /api/build` is also public. `GET /api/providers/kroger/callback` stays behind the login. The browser sends the SameSite=Lax session cookie on the redirect from Kroger. `/favicon.ico`, `/health`, and every path that is not listed as public still require the password. If the password file is missing, the public hostname refuses API calls with `401` and still serves the login page, which cannot sign in. The `sudo ./setup.sh` above is what puts that Caddyfile in front of the site. An image update does not reload it.

To change the password, edit `BASIC_AUTH_PASSWORD`, remove `/opt/pantry/auth.caddy`, and run `sudo ./setup.sh` again. Leaving `auth.caddy` in place keeps the current hash, so a routine setup does not regenerate it or ask you to type the password again. The login page and any script that sends `Authorization: Basic` both check that hash. Phones stay signed in until the session secret changes or someone uses Log out.

`sudo ./setup.sh unpublish` stops only the proxy. The LAN site keeps running. Clear `PUBLIC_HOST` as well if an automatic-update timer is enabled, or the next `sudo ./setup.sh` will start the proxy again.

### Home Wi-Fi hangs on the public name

On the certificate path, `https://stockbot.rhionin.com` loads on cellular and sits there on home Wi-Fi until the browser gives up. Caddy and Stockbot are up the whole time. [Cloudflare Tunnel](#cloudflare-tunnel) is how the same name loads on both. The rest of this section is the port-forward behavior. The phone on Wi-Fi looks up the public DNS A record, gets the router's WAN address (`38.148.49.79` in this house), and sends the connection to the Gryphon. Cellular never does that: it is already outside the house, so the same port forward delivers it to the Pi.

That inside-the-house path is NAT hairpin (also called NAT loopback or NAT reflection). The router has to send a connection aimed at its own WAN address back to `192.168.1.203`. Many home routers drop or ignore that connection instead of refusing it, so the browser hangs rather than showing an error. Nothing on the Pi can answer a packet that never arrives. This repository does not change the Gryphon.

**Use this on home Wi-Fi.** It does not go through the router, and it does not ask for the shared password:

| From a phone on this Wi-Fi | What it is |
|-----------------------------|------------|
| `http://192.168.1.203:8080` | Stockbot on the Pi's LAN address. `sudo ./setup.sh status` prints the address it detected. |
| `http://pantry.local:8080` | The same port, published with mDNS when Avahi is installed. |

`192.168.1.203` is this Pi's reserved LAN address. If `status` prints a different address, use that one. If it prints a Docker bridge (often `172.17.0.1`), set `PANTRY_LAN_IPV4` to the Pi's real LAN address in `/opt/pantry/.env` and run `sudo ./setup.sh` again. Do not forward port 8080. That listener has no password.

Raspberry Pi OS usually already runs Avahi (`raspberrypi.local`). `sudo ./setup.sh` adds `pantry.local` and an HTTP service advertisement when `avahi-publish-address` is on the Pi. If status never mentions a published name:

```bash
sudo apt-get install -y avahi-daemon
sudo ./setup.sh
```

Phones and computers that resolve `.local` (current iOS, Android, macOS, and Windows) can then open `http://pantry.local:8080` without knowing the IP. A DHCP reservation for the Pi still matters: mDNS follows the address setup detected, and a later address change is picked up the next time `pantry-mdns.service` starts (`sudo ./setup.sh` restarts it).

**Gryphon, if you want the public name itself to work on Wi-Fi.** Gryphon Connect is the only admin. Gryphon does not ship a browser admin page. This deploy does not turn anything on in that app.

1. In the Gryphon Connect app, confirm the Pi's DHCP reservation is still `192.168.1.203` (or whatever `PANTRY_LAN_IPV4` is).
2. Confirm the existing forwards, which are what make cellular work: external TCP 80 to the Pi's TCP 80, and external TCP 443 to the Pi's TCP 443. Leave UDP 443 optional. Do not add 8080.
3. Look through the app's network and advanced screens for NAT loopback, NAT reflection, or hairpin NAT. Gryphon's public help does not document that switch. If it is there, enable it for those two forwards and try `https://stockbot.rhionin.com` on Wi-Fi again.
4. If the switch is not there, ask Gryphon support from the in-app chat to enable NAT loopback for the existing 80 and 443 forwards. Until they do, home Wi-Fi keeps using the LAN address above. The public name, the Let's Encrypt certificate, and the shared password stay as they are for everyone off the LAN.

**Same hostname on the LAN, without hairpin.** Caddy is already listening on the Pi's port 443. If a client resolves `stockbot.rhionin.com` to the Pi's LAN address, it gets the real certificate and the same shared password. Internet DNS is left pointing at the WAN address, so cellular and other outside clients are unchanged.

That resolution is split-horizon DNS. It only helps devices that query the Pi. Gryphon often answers DNS itself for filtering, and the Connect app does not document a house-wide DNS server field. If a device cannot be pointed at the Pi, skip this and use the LAN URL.

To turn the answerer on:

```bash
sudo apt-get install -y dnsmasq
```

In `/opt/pantry/.env`:

```bash
PANTRY_LAN_IPV4=192.168.1.203
PANTRY_SPLIT_DNS=on
```

Then `sudo ./setup.sh`. It writes a dnsmasq config that answers only this hostname with the LAN address, forwards every other name to 1.1.1.1 and 9.9.9.9, and listens on the LAN address rather than on every interface. It does not offer DHCP. Do not forward port 53 on the Gryphon. Devices that use the Pi for DNS skip Gryphon's filter for those other lookups. Leave `PANTRY_SPLIT_DNS` off when that filter should keep covering the house, and use the LAN address instead.

Check from a computer that is using the Pi as its DNS server:

```bash
dig +short stockbot.rhionin.com A @192.168.1.203
```

The answer has to be `192.168.1.203`. If a phone on Wi-Fi still resolves the name to the WAN address, the phone is not using the Pi, and `https://stockbot.rhionin.com` will keep hanging. Set `PANTRY_SPLIT_DNS=off` and run `sudo ./setup.sh` to remove the config.

A computer can also pin the name without dnsmasq, by adding a line to its hosts file (`192.168.1.203 stockbot.rhionin.com`). Phones generally cannot edit a hosts file. Use the LAN URL on those.

### When something fails

Logs from the proxy:

```bash
cd /opt/pantry
sudo docker compose --profile public logs --tail=80 caddy
```

| What you see | What to fix |
|--------------|-------------|
| `NXDOMAIN`, or `dig` returns no address | The Dynu A record is missing or still cached (or, if nameservers are not Dynu's, the record at the DNS host that is). Wait, then check `dig +short stockbot.rhionin.com A` again. |
| Certificate error mentioning timeout, connection refused, or `404` from another site | Port 80 is not reaching this Pi. Re-check the router forward and that no other program is bound to port 80. |
| Browser warning, certificate name mismatch | `PUBLIC_HOST` and the Squarespace host are not the same name. They must match exactly. |
| `https://` works at home but not on cellular | The phone is still using the LAN address, or the forward is wrong. Test on cellular. |
| `https://` hangs on home Wi-Fi and loads on cellular | The router is not hairpinning. Stockbot is reachable at `http://<pi-ip>:8080` and, when Avahi is installed, `http://pantry.local:8080`. See [Home Wi-Fi hangs on the public name](#home-wi-fi-hangs-on-the-public-name). |
| Browser shows the login page, or curl gets `401` JSON | The shared password is missing or does not match the hash in `auth.caddy`. A request with no password is supposed to be rejected, except `GET /api/telemetry`, `POST /api/telemetry/client`, `GET /api/build`, `GET /brand/logo.png`, `GET /terms`, `GET /privacy`, `POST /api/deploy-hook`, and `POST /api/sentry-tunnel`, which are public. A browser page load shows the login page. An API call with no cookie and no `Authorization: Basic` header returns `401` JSON. Edit `BASIC_AUTH_PASSWORD`, remove `/opt/pantry/auth.caddy`, and run `sudo ./setup.sh`. Do not delete `auth/household` by itself; setup copies it from `auth.caddy`. |
| UI loads, but the scan queue never updates live | `/api/events` is being buffered. `deploy/Caddyfile` must keep `flush_interval -1` on that path. Run `sudo ./setup.sh` after pulling a fresh `Caddyfile`. |

### Cloudflare Tunnel

This is the way `https://stockbot.rhionin.com` works on home Wi-Fi and on cellular when the Gryphon does not hairpin, without forwarding ports 80 or 443. The Pi opens an outbound connection to Cloudflare. The tunnel **pantry-pi** (`9e158cce-4d31-4881-82f3-d905af6164e7`) already exists and is remotely managed, so the Pi only runs `cloudflared` with `CLOUDFLARE_TUNNEL_TOKEN`. Do not create a second tunnel, and do not add a local ingress file. An empty token leaves Let's Encrypt, the Gryphon forwards, and the Dynu dynamic DNS client exactly as they are. The login page asks for the same household password, and these paths stay public: `GET /api/telemetry`, `POST /api/telemetry/client`, `GET /api/build`, `GET /brand/logo.png`, `GET /terms`, `GET /privacy`, `POST /api/deploy-hook`, and `POST /api/sentry-tunnel`. Do not point the tunnel at port 8080. That port has no password. Do not turn on Cloudflare Access for this hostname. The login page already asks for the password, and Access would be a second login.

LAN `http://192.168.1.203:8080` and `http://pantry.local:8080` keep working the whole time, with no password. Use them if the public name is down during the cutover.

The token is a secret. It lives only in `/opt/pantry/.env`. Do not commit it, and do not wrap it in quotes.

#### 1. Add the domain on the Free plan

1. Use the Cloudflare account that already owns tunnel **pantry-pi**. Stay on the **Free** plan. Tunnels are included. You do not need Zero Trust paid seats for this.
2. Add the site `rhionin.com` if it is not already there. Cloudflare imports the DNS records it can see. Those records are served by Dynu right now.

Do not change nameservers until the next step checks the import. Do not create a tunnel.

#### 2. Check the imported records, and turn DNSSEC off

Before the domain uses Cloudflare's nameservers, compare the imported records with Dynu. A missing mail or website record is a silent outage after the switch.

Open the Dynu control panel and write down:

- The website records: A and CNAME for `@` and `www`, and the dynamic A record Dynu updates for this house, including `pantry` if it is a separate name
- Every MX record
- Every TXT record (mail SPF, DKIM, DMARC, site verification, and anything else Dynu shows)

In Cloudflare's DNS tab, confirm each of those is present and the values match. Fix Cloudflare's copy before continuing. Leave the `pantry` A record pointing at the home address for now. That keeps cellular working after the nameserver change, until the tunnel hostname replaces it.

DNSSEC has to be off before the nameservers change. Turn it off in Dynu if it is enabled there, and at the registrar (Squarespace Domains, if that is where the nameservers are set) if it is enabled there too. Wait until it shows disabled. Changing nameservers while DNSSEC is still on can make `rhionin.com` stop resolving.

#### 3. Change nameservers from Dynu to Cloudflare

Cloudflare shows two nameservers when you add the site. Copy them. The registrar is where the nameservers are changed. If that is Squarespace: **Domains → rhionin.com → DNS → Nameservers → use custom nameservers**, and paste Cloudflare's two. Registration stays put. DNS for the whole domain moves off Dynu, which is why step 2 has to match the Dynu zone.

Wait until Cloudflare says the site is active. Check from the Pi:

```bash
dig +short NS rhionin.com
```

The answers should be Cloudflare's nameservers, not Dynu's. The Dynu update client can keep running; it no longer affects this name once the nameservers have moved. The existing public A record (`stockbot`, or `pantry` until that name is removed), now served by Cloudflare, still points at the home address, so cellular keeps working through the Gryphon forwards. Home Wi-Fi still hangs on that name. That is expected until the tunnel hostname replaces the A record.

#### 4. Copy the token from pantry-pi

1. In the Cloudflare dashboard, open **Zero Trust**.
2. Go to **Networks → Tunnels**.
3. Open **pantry-pi** (`9e158cce-4d31-4881-82f3-d905af6164e7`). Do not create a tunnel.
4. On its overview, copy the token from the Docker install command (the value after `--token`). Do not run that command on the Pi. `setup.sh` starts `cloudflared`.

You will put that token in `/opt/pantry/.env` as `CLOUDFLARE_TUNNEL_TOKEN`. Do not run setup yet. The site is still on the A record.

#### 5. Set the public hostname service URL

On **pantry-pi**, open **Routes** (or **Public Hostname**). Add or edit the published application for `stockbot.rhionin.com`. This is the remotely managed ingress. Nothing on the Pi stores it.

| Field | Value |
|-------|--------|
| Subdomain | `stockbot` |
| Domain | `rhionin.com` |
| Path | leave empty |
| Type | HTTP |
| URL | `http://caddy:80` |

`http://caddy:80` is how `cloudflared` reaches Caddy. Both containers share the Compose network `cf-tunnel`. The tunnel Caddy service is `caddy-tunnel`, and that network gives it the DNS name `caddy`. Its HTTP listener is port 80 inside the network. It is not a port on the Pi, and it is not port 8080. If the form asks for a type separately, choose **HTTP** and enter `caddy:80` so the scheme is not doubled. The public side is HTTPS with Cloudflare's certificate. Caddy sees the original `https` scheme and the visitor address only because that connection comes from cloudflared (`10.77.77.2`).

Saving this hostname replaces the `stockbot` A record with the tunnel. The public name stops working until the Pi is running cloudflared. Have the next command ready, then save the hostname. The tunnel may already publish `pantry.rhionin.com`. Leave that route in place and follow [Previous public hostname](#previous-public-hostname).

#### 6. Put the token on the Pi and switch

On the Pi, edit `/opt/pantry/.env`:

```bash
PUBLIC_HOST=stockbot.rhionin.com
PUBLISH_MODE=tunnel
CLOUDFLARE_TUNNEL_TOKEN=paste-the-token-here
BASIC_AUTH_USER=pantry
BASIC_AUTH_PASSWORD=replace-with-a-long-passphrase
```

`ACME_EMAIL` can stay. Tunnel mode does not request a certificate. Keep `BASIC_AUTH_PASSWORD` as it already is if `/opt/pantry/auth.caddy` exists. Setup will not regenerate that file. Leave the Dynu client installed. With the token set, Stockbot no longer depends on it, and emptying the token later does not restart or stop that client.

```bash
sudo ./setup.sh publish --tunnel
```

That is the command that switches modes. A later `sudo ./setup.sh` stays on the tunnel as long as the token is set, which is also what the automatic-update timer does. The command copies the tunnel compose file, starts `cloudflared` with `tunnel run`, and starts Caddy on an internal HTTP port only. Nothing on the Pi listens on 80 or 443. The shared password is unchanged. The login page replaces the browser prompt, and the public paths stay public.

If `PANTRY_SPLIT_DNS` was on, this run removes it. A LAN answer for `stockbot.rhionin.com` would send phones to the Pi, and Caddy is no longer on port 443 there. Leave `PANTRY_SPLIT_DNS=off`.

#### 7. Test on home Wi-Fi and on cellular

From a phone on home Wi-Fi, open `https://stockbot.rhionin.com`, sign in on the login page with the household password, and confirm the pantry loads. Repeat on cellular data, with Wi-Fi off. Both should load. Neither should hang. A phone that already signed in stays signed in.

```bash
curl -fsS -u 'pantry:replace-with-a-long-passphrase' https://stockbot.rhionin.com/health
curl -fsS https://stockbot.rhionin.com/api/telemetry
```

`/health` without the password is `401` JSON. `/api/telemetry` and `/api/build` without the password are JSON. The brand mark, terms, and privacy URLs are still public. `GET /api/providers/kroger/callback` stays behind the login; the browser sends the session cookie on the way back from Kroger. Tunnel mode reads the visitor address from `Cf-Connecting-Ip`, so one person cannot pause sign-in for everyone else.

`http://192.168.1.203:8080` still does not ask for the password. Do not forward it.

#### 8. Remove the Gryphon forwards

Do this only after both networks load the public name. In Gryphon Connect, delete the forwards for external TCP 80 and external TCP 443 to `192.168.1.203`. Delete UDP 443 too if it was added. Do not add a forward for 8080.

If you previously ran `sudo ufw allow 80/tcp` and `sudo ufw allow 443/tcp`, you can delete those allows. Leave a firewall that is currently inactive turned off.

#### Previous public hostname

`https://stockbot.rhionin.com` is the public site. `pantry.rhionin.com` is the previous name. `sudo ./setup.sh` does not replace a `PUBLIC_HOST` that is already set, so a Pi that still says `pantry.rhionin.com` keeps serving that name until you edit `/opt/pantry/.env`:

```bash
PUBLIC_HOST=stockbot.rhionin.com
LEGACY_PUBLIC_HOST=pantry.rhionin.com
```

Then run `sudo ./setup.sh`. Caddy serves the new name. Requests for the old name get a 308 redirect to `https://stockbot.rhionin.com` with the same path and query. On the certificate path, Caddy also requests a certificate for the old name so that redirect can be HTTPS. Tunnel mode does not. Cloudflare's certificate covers the name, and Caddy only sees HTTP from cloudflared. Clear `LEGACY_PUBLIC_HOST` and run `sudo ./setup.sh` again when that redirect should stop. A value equal to `PUBLIC_HOST`, or a value that includes `https://`, is ignored and the old name is not a second site.

The session cookie is for one hostname. A phone that was signed in on `pantry.rhionin.com` signs in again on `stockbot.rhionin.com`. Scan-alert subscriptions belong to the site that created them. Open the new name and allow notifications again. Leave the VAPID keys and `VAPID_SUBJECT` as they are. Setup does not rotate them.

Cloudflare still owns the tunnel routes. Nothing in this repo calls the Cloudflare API. On the existing tunnel **pantry-pi** (`9e158cce-4d31-4881-82f3-d905af6164e7`):

1. Add a public hostname for `stockbot.rhionin.com`. Subdomain `stockbot`, domain `rhionin.com`, path empty, type HTTP, URL `http://caddy:80`. Do not create a second tunnel. Do not point it at port 8080. Do not turn on Cloudflare Access.
2. Leave `pantry.rhionin.com` on that same tunnel with the same URL, so Caddy can redirect it.
3. On the Pi, set the two variables above and run `sudo ./setup.sh`. Until `PUBLIC_HOST` is `stockbot.rhionin.com`, Caddy does not answer that Host header.
4. Open `https://stockbot.rhionin.com` on home Wi-Fi and on cellular, sign in, and confirm a scan shows up. Then open `https://pantry.rhionin.com/api/build` and confirm the response redirects to `https://stockbot.rhionin.com/api/build`.

GitHub Actions posts `POST /api/deploy-hook` to `https://stockbot.rhionin.com` first. If that request fails, it tries `https://pantry.rhionin.com`. The one-minute timer still pulls the image if both fail. After the new name answers, the fallback is unused. A redirect on the old name does not accept the hook; the primary URL has to answer.

Empty `LEGACY_PUBLIC_HOST`, run `sudo ./setup.sh`, and delete the `pantry.rhionin.com` public hostname in the tunnel when you retire the old name.

#### Rollback

Smaller rollback, DNS stays at Cloudflare:

1. Put the Gryphon TCP 80 and TCP 443 forwards back, to the Pi, before changing DNS.
2. In the tunnel, delete the public hostnames `stockbot.rhionin.com` and `pantry.rhionin.com`.
3. In Cloudflare DNS, add an A record: name `stockbot`, value the address from `curl -4 https://ifconfig.me` on the Pi.
4. On the Pi, empty `CLOUDFLARE_TUNNEL_TOKEN` in `/opt/pantry/.env`. Leave `PUBLIC_HOST=stockbot.rhionin.com`. Set `PUBLISH_MODE=acme` or leave it empty. A non-empty token keeps tunnel mode, so the token line has to be empty. Set `ACME_EMAIL` if it is empty.
5. Run `sudo ./setup.sh`. That stops cloudflared and starts Caddy on ports 80 and 443 again. The certificate volume from the earlier certificate path is still there.
6. Test on cellular. Home Wi-Fi hangs on the public name again. Use `http://192.168.1.203:8080` or `http://pantry.local:8080` on that network.
7. Leave the **pantry-pi** tunnel in the dashboard. Emptying the token stops only the connector on the Pi.

To move DNS back to Dynu, after the smaller rollback is working, copy every record Cloudflare is serving (website, mail, and `stockbot`) into Dynu first, and point `stockbot` at the WAN address again. Copy `pantry` too if that name is still published. Then set the registrar nameservers back to Dynu's, confirm `dig +short NS rhionin.com` and `dig +short stockbot.rhionin.com A`, and turn the Dynu update client back on if you stopped it. Only then turn DNSSEC back on if you want it. With the token empty, that dynamic DNS client is what keeps the port-forward site on the current home IP.

`sudo ./setup.sh unpublish` stops the proxy and cloudflared and leaves the LAN site up. Clear `PUBLIC_HOST` and `CLOUDFLARE_TUNNEL_TOKEN` as well when the update timer is enabled, or the next update starts the tunnel again.

### When port forwarding cannot work

**Cloudflare Tunnel** is wired in. Use [Cloudflare Tunnel](#cloudflare-tunnel) when the Gryphon does not hairpin or when you cannot forward ports. The service URL is `http://caddy:80` on the existing tunnel pantry-pi. Do not also run the certificate path in front of the same hostname. With the token empty, Dynu dynamic DNS and the port forwards stay as they are.

**Tailscale Funnel** (fits a stable URL when you do not need `rhionin.com`) is not wired into this repo. The free personal tier can expose the Pi as a `*.ts.net` name without opening ports. Putting a Squarespace name on Funnel is more work than the Caddy path; use Funnel when a Tailscale hostname is enough. Do not funnel port 8080; that port has no password. Funnel the Caddy port, or put Tailscale in front of the LAN address and skip Funnel.

### What stays exposed if you forward ports

Forwarding 80 and 443 to a computer in the house is a different risk from a VPN or a tunnel. The Pi is on your LAN. Anyone who gets past the shared password is on a process that can read the pantry database, change inventory, and, if you connected Kroger, use the refresh token stored in that database. A bug or a guessed password is not confined to a cloud VM. A tunnel or Tailscale does not put a listening port on the home router; the router path does.

The shared password is one secret for the whole household. The login page applies it to every path on the public hostname except `GET /api/telemetry`, `POST /api/telemetry/client`, `GET /api/build`, `GET /brand/logo.png`, `GET /terms`, `GET /privacy`, `POST /api/deploy-hook`, and `POST /api/sentry-tunnel`. The telemetry paths are public so the timing snapshot can be read without credentials. The snapshot has no barcodes, product names, or user ids. `GET /api/build` is public so an agent can read the running commit, build time, and version; see [Which build is running](#which-build-is-running). The brand mark and the legal pages are public because a grocery developer app fetches those exact URLs. The deploy hook is public so GitHub Actions can reach it through the tunnel; the app still rejects a request that fails the HMAC check. `POST /api/sentry-tunnel` is public so the login page can report a failure. It is rate-limited per visitor and globally, and it forwards an envelope only when the project id is listed. The contract is in `cmd/server/README.md`. The Kroger callback stays behind the login. Inventory, scans, shopping, other static files, `/health`, and `/api/events` stay behind the password. A browser that is not signed in sees the login page. An API call that is not signed in gets `401` JSON. A client that sends `Authorization: Basic` with the same username and password is signed in for that request. Five wrong attempts from one address pause sign-in for 15 minutes. That pause does not follow the username, and a browser that already has a session cookie keeps working. A missing password file refuses the public hostname instead of leaving it open. The password still needs to be long (12 to 72 characters, and longer is better). The LAN address `http://<pi-ip>:8080` never asks for it. That is deliberate. Do not publish that port.

Kroger client secrets and refresh tokens are stored in `pantry.db` as plain text. The HTTP API does not return them. A copy of the database (a backup, or the Docker volume) does. Treat `pantry.db` like a password file. The login page protects the public hostname only. Port 8080 has no login: if something forwards that port, every route is open, including wiping inventory (the body must contain `WIPE INVENTORY`) and saving a Kroger client secret.

Automatic updates run as root and pull `latest` when you enable the timer. A bad image then starts on the same Pi that is reachable from the internet. Leave the timer off unless you want that.

Prefer a tunnel or Tailscale when you do not need a public listener. If you keep the port forward: only TCP 80 and 443, DHCP reservation, UPnP off, no forward of 8080. `sudo ./setup.sh` already limits the Stockbot port to LAN sources, so a mistaken forward of that port still has to come from the LAN unless `PANTRY_LAN_FIREWALL=off`.

### Keeping HTTPS across automatic updates

`pantry-update.sh` includes the `public` profile only when `PUBLIC_HOST` is set and `auth.caddy` exists, so a timer pull renews the proxy instead of forgetting it. When `CLOUDFLARE_TUNNEL_TOKEN` is set, the timer uses the `tunnel` profile instead and does not publish ports 80 or 443. `sudo ./setup.sh` copies the updated unit into `/etc/systemd/system/` if that unit is already installed. If you enabled the timer before this change and have not run setup yet:

```bash
sudo cp /opt/pantry/systemd/pantry-update.service /etc/systemd/system/
sudo systemctl daemon-reload
```

## Manual Update Procedure

To update to the latest version:

```bash
cd /opt/pantry
sudo docker compose pull
sudo docker compose up -d
```

Docker Compose will automatically:
- Download the new image if available
- Recreate the container only if the image changed
- Preserve your data volume across updates
- Apply your `.env` configuration to the new container

Recreating the container briefly drops the scanner session, but the listener reconnects on its own within 30 seconds — no manual step is needed.

## Configuration

All configuration is handled through environment variables in `/opt/pantry/.env`. Copy from `.env.example` and modify as needed:

| Variable | Default | Description |
|----------|---------|-------------|
| `PANTRY_IMAGE_TAG` | `latest` | Container image tag to deploy. Use `latest` for newest build, `master` for master branch, or full commit SHA to pin version |
| `HOST_PORT` | `8080` | IPv4 port for the LAN site. The container still listens on 8080. Published on `0.0.0.0` only, not IPv6. Do not forward this port on the router |
| `PUBLIC_HOST` | empty | Hostname for the public HTTPS proxy, such as `stockbot.rhionin.com`. Empty keeps the install LAN-only. No `https://`. Setup does not replace a value that is already set |
| `LEGACY_PUBLIC_HOST` | empty | Previous hostname that redirects to `PUBLIC_HOST`, such as `pantry.rhionin.com`. Empty serves `PUBLIC_HOST` only. Setup writes the redirect and ignores a value that matches `PUBLIC_HOST` or includes a scheme |
| `PUBLISH_MODE` | empty | `tunnel` selects Cloudflare Tunnel and requires `CLOUDFLARE_TUNNEL_TOKEN`. Empty or `acme` is the certificate path, unless that token is set — a non-empty token selects tunnel on its own |
| `CLOUDFLARE_TUNNEL_TOKEN` | empty | Token for the existing tunnel pantry-pi. Empty keeps Let's Encrypt, the Gryphon forwards, and the Dynu update client. Secret. The hostname's service URL is `http://caddy:80` |
| `ACME_EMAIL` | empty | Email Let's Encrypt uses for certificate expiry notices. Required when `PUBLIC_HOST` is set and the token is empty. Not used in tunnel mode. Not a Stockbot login |
| `BASIC_AUTH_USER` | `pantry` | Username the login page asks for on the public site. Also accepted in an `Authorization: Basic` header |
| `BASIC_AUTH_PASSWORD` | empty | Shared password for the public site, 12 to 72 characters. Required the first time the public proxy starts, unless `auth.caddy` already exists. The hash is written to `auth.caddy` and copied to `auth/household`; this value stays in `.env`. Setup does not replace an existing `auth.caddy` |
| `PANTRY_SESSION_SECRET` | empty | Signs the login cookie. Setup and the update timer fill it in once when `auth.caddy` exists, and do not rotate it. Leave it alone so phones stay signed in |
| `VAPID_PUBLIC_KEY` | generated | Public half of the Web Push identity for scan alerts while Stockbot is closed. `sudo ./setup.sh` fills it once. Not a secret |
| `VAPID_PRIVATE_KEY` | generated | Private half of that identity. A secret. Setup fills it once and does not rotate a pair that is already set |
| `VAPID_SUBJECT` | generated | Contact on each push, `mailto:` or `https://`. Setup uses `mailto:pantry@PUBLIC_HOST`, or `mailto:pantry@localhost` when the install is LAN-only |
| `SENTRY_DSN` | empty | Error reporting. Empty keeps it off, including on the Pi, and the process makes no error-reporting calls. Set this in `/opt/pantry/.env` only. That file is mode `0600` and owned by root, so editing it needs `sudo` |
| `SENTRY_ENVIRONMENT` | empty | Optional label sent with errors. Empty means `production` when `SENTRY_DSN` is set |
| `SENTRY_TUNNEL_PROJECT_IDS` | empty | Comma-separated project ids `POST /api/sentry-tunnel` may forward. Empty leaves that route hidden |
| `SENTRY_TUNNEL_PUBLIC_KEYS` | empty | Optional comma-separated DSN public keys the tunnel accepts. Empty does not check the key. Set this in `/opt/pantry/.env` only |
| `PANTRY_LAN_FIREWALL` | `on` | `off` leaves `HOST_PORT` reachable from any source. Any other value, including empty, installs the LAN-only rule when compose publishes that port or the public proxy is on |
| `PANTRY_LAN_IPV4` | empty | Private LAN address of the Pi, such as `192.168.1.203`. Empty detects it. Set this when detection prints a Docker bridge instead of the address phones should open |
| `PANTRY_SPLIT_DNS` | `off` | `on` answers `PUBLIC_HOST` with `PANTRY_LAN_IPV4` from dnsmasq on the Pi. Public DNS and the shared password stay as they are. Leave off unless clients use the Pi for DNS. Do not forward port 53 |
| `PRODUCT_CACHE_TTL` | `720h` | How long to cache product lookups (720h = 30 days) |
| `PRODUCT_MISS_TTL` | `168h` | How long to cache "not found" results (168h = 7 days) |
| `DISABLE_EXTERNAL_PRODUCT_LOOKUP` | `false` | Set to `true` to disable external API calls for product information. This also keeps product contribution local |
| `PANTRY_RETAILER_API_KEY` | empty | Store price API key. Leave empty until a retailer adapter and credentials exist. Noted sales in the app still apply |
| `PANTRY_RETAILER_API_URL` | empty | Reserved base URL for that store price adapter |
| `PRODUCT_OPENER_USER_ID` | empty | Optional. Account used only after someone opts in to share a product. Leave empty to keep contributions on this pantry |
| `PRODUCT_OPENER_PASSWORD` | empty | Optional password for that account. Each open database has its own users |
| `STOCK_IN_CONTROL_BARCODE` | `STOCK_IN` | Barcode to scan for switching scanner to "stock in" mode |
| `STOCK_OUT_CONTROL_BARCODE` | `STOCK_OUT` | Barcode to scan for switching scanner to "stock out" mode |
| `HEADLESS_USER_ID` | `user-1` | User ID for headless scan operations (when no user is logged in) |
| `SCANNER_DEVICE` | `/dev/input/pantry-scanner` | Path to the scanner's stable symlink created by the udev rule. Point at a concrete `/dev/input/eventN` to bypass the symlink while debugging |
| `SCANNER_GID` | `65532` | Numeric group ID the container process joins so it can read the `0640` scanner node |
| `DEPLOY_HOOK_SECRET` | generated | Shared HMAC secret for `POST /api/deploy-hook`. `sudo ./setup.sh` fills this in when it is empty and never replaces a value already set. The same value is the GitHub Actions secret `DEPLOY_HOOK_SECRET`. Print it with `sudo ./setup.sh deploy-secret` |
| `GROCY_URL` | empty | Grocy base URL from the sidecar, usually `http://grocy:80`. Empty keeps grocy-sync off. See [Grocy sync](#grocy-sync) |
| `GROCY_API_KEY` | empty | API key created in the Grocy UI. Stored only in `/opt/pantry/.env` |
| `PANTRY_SYNC_TOKEN` | empty | Read-only secret the sidecar sends to Stockbot. Generate with `openssl rand -hex 32` |
| `GROCY_SYNC_INTERVAL` | `5m` | How often the sidecar re-reads Stockbot and writes Grocy |
| `TZ` | empty | Time zone for stock dates. Set it to the same value as the Grocy container |

**Note:** The variables `ADDR` and `DB_PATH` are pinned by the Docker Compose file and should not be overridden. To change the host port, use `HOST_PORT` instead of modifying `ADDR`.

After changing configuration:

```bash
cd /opt/pantry
sudo docker compose up -d
```

Setting `PUBLIC_HOST` does not publish the site by itself. Run `sudo ./setup.sh` so the `public` profile starts when `ACME_EMAIL` is set and `auth.caddy` exists (or `BASIC_AUTH_PASSWORD` is set and `auth.caddy` does not). A plain `docker compose up` leaves that profile off. With `CLOUDFLARE_TUNNEL_TOKEN` set, `sudo ./setup.sh` starts the `tunnel` profile instead. `ACME_EMAIL` is not required then.

## Headless Scanner Input

Stockbot reads barcode scans directly from the scanner's evdev device (`/dev/input/eventN`) instead of standard input, so it works headlessly without a keyboard, monitor, or attached terminal session.

**The container starts whether or not the scanner is attached, and the scanner may be connected or disconnected at any time.** There is no requirement to install the udev rule or attach the scanner before starting the container.

### How hot-plug works

- The container bind-mounts the `/dev/input` **directory** rather than a single device node. Nodes that udev creates later — when you plug the scanner in — become visible inside the running container automatically, with no recreation.
- The application retries opening the device with exponential backoff, from 250 ms up to a 30 s cap. A scanner that appears (at boot, on plug-in, or on Bluetooth wake) is picked up **within 30 seconds** with no operator action.
- The web UI is served the entire time, independent of scanner state. Disconnecting the scanner does not stop, restart, or recreate the container.

### Prerequisites

1. **Find your scanner's device information:**

   **USB scanners:**
   ```bash
   lsusb | grep -i scanner
   ```
   Output looks like: `Bus 001 Device 005: ID XXXX:YYYY Symbol Technologies, Inc. Scanner`. The `XXXX` is the vendor ID and `YYYY` is the product ID.

   **Bluetooth scanners:**
   Bluetooth scanners appear as input devices and won't show in `lsusb`. Instead:
   ```bash
   # List all input event devices
   ls -l /dev/input/event*

   # Get details about a specific event device (replace eventX with your device)
   udevadm info -a -p $(udevadm info -q path -n /dev/input/eventX) | grep -E "(vendor|product|name)"

   # Or look for your scanner in the input device list
   cat /proc/bus/input/devices | grep -A5 -B5 -i scanner
   ```

2. **Install the udev rule.** The primary method is the setup script, which detects the device and writes the rule for you:
   ```bash
   sudo ./setup.sh rule
   ```

   **Manual fallback.** Copy the example rule and edit it:
   ```bash
   sudo cp /opt/pantry/udev/99-pantry-scanner.rules /etc/udev/rules.d/
   ```

   Uncomment the example and set the vendor/product IDs. The same rule covers USB HID and Bluetooth HID. It matches the input parent's `ATTRS{id/vendor}` and `ATTRS{id/product}` (the `Vendor=` / `Product=` values from `/proc/bus/input/devices`). `ENV{ID_VENDOR_ID}` and `ENV{ID_MODEL_ID}` are imported only when the device has a USB ancestor, so they do not match a Bluetooth scanner:
   ```
   SUBSYSTEM=="input", KERNEL=="event*", ATTRS{id/vendor}=="XXXX", ATTRS{id/product}=="YYYY", \
     SYMLINK+="input/pantry-scanner", GROUP="65532", MODE="0640"
   ```

   If the device has no vendor and product IDs, match by name instead:
   ```
   SUBSYSTEM=="input", KERNEL=="event*", ATTRS{name}=="*Scanner*", \
     SYMLINK+="input/pantry-scanner", GROUP="65532", MODE="0640"
   ```
   Replace `*Scanner*` with the actual name pattern from your device (found in step 1).

   Note the symlink is `input/pantry-scanner`, which resolves to `/dev/input/pantry-scanner`. It **must** live under `/dev/input` because the container bind-mounts only that directory; a symlink elsewhere would neither exist nor resolve inside the container.

3. **Reload udev and trigger:**
   ```bash
   sudo udevadm control --reload
   sudo udevadm trigger --subsystem-match=input --action=add
   ```

4. **Verify `/dev/input/pantry-scanner` exists:**
   ```bash
   ls -l /dev/input/pantry-scanner
   ```
   The symlink should exist with GID 65532 on its target node.

5. **Ensure `SCANNER_DEVICE` is set** (defaults to `/dev/input/pantry-scanner`) in `.env`.

6. **Verify scanner status:**
   ```bash
   curl http://localhost:8080/health
   ```
   Look for the `scanner` object in the response:
   - `connected: true` and `grabbed: true` means the scanner is working
   - `connected: false` is a fully supported state — the container still runs and the UI still works; the scanner is simply absent or not yet readable

### Security: per-device scoping

Per-device scoping now comes from the scanner node's **ownership and mode**, not from the container seeing only one node. The container bind-mounts all of `/dev/input`, but the udev rule grants only the scanner's node `GROUP="65532"` mode `0640`. Every other input node stays `root:input` mode `0600`, so the container process (uid 65532, group 65532) can open the scanner and nothing else. This is why Stockbot does not become a keylogger for every attached keyboard, and it is why the service user is deliberately **not** added to the `input` group — doing so would grant read access to every input device on the box.

### Local Development

For local development without a scanner, use stdin mode:
```bash
SCAN_INPUT=stdin go run ./cmd/server
```

Type barcodes directly into the terminal and press Enter. The scanner status will show `connected: false` since there's no device, but scans will still work.

### Troubleshooting

First, distinguish the two very different situations that both show `connected: false`:

- **Scanner absent** — no scanner is plugged in. This is a fully **supported** state: the container is running, `/health` returns `status: ok`, and the web UI works. Nothing is wrong. Plug the scanner in and it is picked up within 30 seconds.
- **Scanner present but unreadable** — the scanner is attached but Stockbot cannot read it. This is the case worth investigating. The `lastError` field tells you which of three causes applies, in the order worth checking:

| `lastError` | Meaning | Fix |
|---|---|---|
| `ENOENT` / no such file | The udev rule did not match, so no symlink was created | Fix the rule's match keys (`ATTRS{id/vendor}`/`ATTRS{id/product}`, or `ATTRS{name}` when those IDs are absent) and re-run `sudo ./setup.sh rule` |
| `EACCES` / permission denied | The node's group is not 65532 | Fix `GROUP` in the rule or `SCANNER_GID` in `.env`, then reload udev |
| `EPERM` / operation not permitted | The device cgroup denied the open | `device_cgroup_rules` is not taking effect — do **not** fall back to `privileged: true` or reintroduce a `devices:` entry; see `ACCEPTANCE.md` |

The `GET /health` `scanner` object also reports:

| Field | Expected Value | Issue if different |
|-------|----------------|-------------------|
| `grabbed` | `true` | Another process is using the device (the exclusive grab failed — capture still works) |
| `unmappedKeys` | Low / zero | Scanner is emitting keycodes outside the US-layout map; consider modifying `internal/scanlistener/keymap.go` |

The image is **distroless**, so `docker exec pantry sh` does not work. Use `sudo ./setup.sh logs`, `sudo docker compose logs pantry`, `GET /health`, and `sudo ./setup.sh status` for all runtime introspection.

### Automatic Updates and the scanner

`pantry-update.service` only updates the container image, never the deployment files. When `PUBLIC_HOST` is set, the update script also refreshes the public HTTPS proxy; see [Keeping HTTPS across automatic updates](#keeping-https-across-automatic-updates). This means:
- The udev rule is installed once (via `sudo ./setup.sh rule`) and is not touched by updates
- Docker Compose and `.env` files are never automatically modified

When an update recreates the container, the scanner session is dropped, but the listener reconnects on its own within 30 seconds. While iterating, run `sudo ./setup.sh freeze` to mask the update timer so an update doesn't change the target mid-experiment; run `sudo ./setup.sh thaw` to restore it.

## Data Management

### Database Location

Stockbot stores its SQLite database in a Docker named volume called `pantry-data`. To inspect the volume:

```bash
sudo docker volume inspect pantry-data
```

### Backup and Restore

**Backup the database:**

```bash
# Create backup
sudo docker cp pantry:/data/pantry.db /home/pi/pantry-backup.db

# Or backup while container is stopped
sudo docker compose down
sudo docker cp pantry:/data/pantry.db /home/pi/pantry-backup.db
sudo docker compose up -d
```

**Restore from backup:**

```bash
# Stop the container
sudo docker compose down

# Restore database
sudo docker cp /home/pi/pantry-backup.db pantry:/data/pantry.db

# Restart
sudo docker compose up -d
```

### Host Directory Access (Alternative)

If you prefer a host directory instead of a named volume, modify `docker-compose.yml`:

```yaml
volumes:
  - /opt/pantry/data:/data
```

Then ensure proper permissions:

```bash
sudo mkdir -p /opt/pantry/data
sudo chown 65532:65532 /opt/pantry/data
```

## Version Management

### Pinning to Specific Versions

To pin to a specific version, set `PANTRY_IMAGE_TAG` in `.env` to a full commit SHA:

```bash
# Example: pin to specific commit
PANTRY_IMAGE_TAG=a1b2c3d4e5f6789012345678901234567890abcd
```

Available tags:
- `latest` - Most recent build from master branch
- `master` - Alias for latest master branch build
- `<commit-sha>` - Specific commit (full SHA)

A pinned tag is preserved across re-runs of `sudo ./setup.sh` and is never silently reset to `latest`.

### Rolling Back

If you need to rollback to a previous version:

1. Find the commit SHA of the working version from the [repository](https://github.com/Rhionin/stockbot)
2. Update `.env`:
   ```bash
   PANTRY_IMAGE_TAG=<previous-commit-sha>
   ```
3. Apply the rollback:
   ```bash
   cd /opt/pantry
   sudo docker compose up -d
   ```

## Automatic Updates (Optional)

For automatic updates, you can install systemd units that periodically check for and apply new releases. `sudo ./setup.sh` does **not** enable them by default, and a later run does not disable a timer you already turned on. Pass `--with-updates` or enable them manually when you are done iterating.

### ⚠️ Important Considerations

**Before enabling automatic updates, understand:**
- The service runs as root because it drives the Docker daemon. The update script does not call `sudo`.
- Enabling automatic updates means **every push to master deploys unattended** with no approval step. Two pushes close together share one publish queue, and `:latest` moves only for the commit that is still the tip of master. See [Deploy when the image is published](#deploy-when-the-image-is-published).
- Container recreation drops the attached scanner session, but the listener reconnects on its own within 30 seconds
- If you pin to a specific commit SHA, leave automatic updates disabled (they would run forever finding nothing)
- A pull that does not change a running image does nothing else. When an image does change, the updater keeps it only if `GET /health` on `127.0.0.1:$HOST_PORT` is ok and `GET /api/build`'s `commit` matches the pantry image label `org.opencontainers.image.revision`. Otherwise it starts the previous image digest again. That does not undo a database migration.

### Installation

1. Confirm Docker path:
   ```bash
   command -v docker
   # Should output: /usr/bin/docker
   ```

2. Install the systemd units. The service runs `/opt/pantry/systemd/pantry-update.sh`, which `sudo ./setup.sh` copies into place and marks executable. When `PUBLIC_HOST` is set and `auth.caddy` exists, that script keeps the HTTPS proxy in the update. The path unit starts that same service when a deploy hook arrives. Or pass `--with-updates` to `sudo ./setup.sh`.
   ```bash
   sudo cp /opt/pantry/systemd/pantry-update.service /etc/systemd/system/
   sudo cp /opt/pantry/systemd/pantry-update.timer /etc/systemd/system/
   sudo cp /opt/pantry/systemd/pantry-update.path /etc/systemd/system/
   sudo systemctl daemon-reload
   ```

3. Enable and start automatic updates:
   ```bash
   sudo systemctl enable --now pantry-update.timer
   sudo systemctl enable --now pantry-update.path
   ```

### Deploy when the image is published

The timer above is the fallback. After `ghcr.io/rhionin/pantry:latest` is pushed, GitHub Actions POSTs a signed request to `https://stockbot.rhionin.com/api/deploy-hook`, and tries `https://pantry.rhionin.com/api/deploy-hook` only if the new name does not answer. Caddy does not ask for the household password on that path. The app checks an HMAC of the timestamp and body, rejects a replay older than five minutes (and a timestamp more than a minute ahead), and writes `/opt/pantry/deploy-trigger/request`. `pantry-update.path` starts `pantry-update.service`, which is the same pull the timer runs. Two hooks that arrive together share one pull; a hook that arrives during a pull causes one more pull. The container cannot call `systemctl` itself. Nothing on the router is opened: the request comes in through the existing Cloudflare tunnel (`cloudflared` to `caddy:80`).

Pushes to `master` publish through one queue, `deploy-master`, with `queue: max`. One image job runs at a time. A publish that has already started is left to finish. Other master publishes wait in that queue, up to GitHub's limit of 100. Jobs join the queue when their tests finish, which can differ from the order the commits were pushed. `queue: max` keeps those waiting publishes, including a newer commit that became ready while an older one was still testing. GitHub cancels a waiting job when that queue is already full. That job did not move `:latest`. Each run still pushes the immutable tag `ghcr.io/rhionin/pantry:<commit>`. It moves `:latest` and `:master`, then calls the deploy hook, only when that commit is still the tip of master. The job writes a notice and a summary line either way. If the pantry tag move fails, that summary says the pantry move failed and that `:latest` and `:master` may be partially moved. A cancel or timeout between the two moves skips that summary line. When a newer commit is already the tip, the job succeeds and leaves those tags where they are. If the tip's run did not publish, re-run it. Re-run only a master run from after #329. An older run uses its own copy of `ci.yml`, which has no concurrency group and no tip guard, so re-running it can move `:latest` backward. `pantry-update.timer` keeps pulling `:latest`, so the Pi follows the tip.

Do this once, after the files from this change are on the Pi:

1. From `deploy/`, run `sudo ./setup.sh`. That generates `DEPLOY_HOOK_SECRET` in `/opt/pantry/.env` when the value is empty, creates `/opt/pantry/deploy-trigger` for uid 65532, and, if the update timer is already installed, installs `pantry-update.path` and applies the 1-minute timer. A secret that is already set is left as it is.
2. If automatic updates are not on yet, run `sudo ./setup.sh --with-updates`. That enables `pantry-update.timer` and `pantry-update.path`.
3. Print the secret:
   ```bash
   sudo ./setup.sh deploy-secret
   ```
4. On GitHub, open the `Rhionin/stockbot` repository, then **Settings → Secrets and variables → Actions → New repository secret**. Name it `DEPLOY_HOOK_SECRET` and paste the printed value. There is no other repository secret to add. The hook URL is `https://stockbot.rhionin.com/api/deploy-hook`. The workflow also tries `https://pantry.rhionin.com/api/deploy-hook` when the new name fails.
5. The next push to `master` that publishes `latest` sends the hook. If `DEPLOY_HOOK_SECRET` is not set on the repository, that workflow step skips and the 1-minute timer still deploys.

`sudo ./setup.sh freeze` masks both the timer and the path unit so a hook does not change the image while you are iterating. `sudo ./setup.sh thaw` turns them back on.

### Management

**Freeze / thaw during iteration:**
```bash
sudo ./setup.sh freeze   # mask the timer and the deploy hook
sudo ./setup.sh thaw     # unmask them when you're done
```

**Check status:**
```bash
# View next scheduled update
sudo systemctl list-timers pantry-update.timer

# Check recent update attempts
sudo journalctl -u pantry-update

# View timer status
sudo systemctl status pantry-update.timer
```

**Disable automatic updates:**
```bash
sudo systemctl disable --now pantry-update.timer pantry-update.path
```

**Manual trigger:**
```bash
sudo systemctl start pantry-update.service
```

### Update Interval

The fallback interval is 1 minute after boot, then every minute. A signed deploy hook starts the same update as soon as the image push finishes; see [Deploy when the image is published](#deploy-when-the-image-is-published). To customize the fallback:

```bash
sudo systemctl edit pantry-update.timer
```

Add:
```ini
[Timer]
OnUnitActiveSec=
OnUnitActiveSec=30min
```

Save and reload:
```bash
sudo systemctl daemon-reload
```

**Note:** The hook is what removes the wait after a push. The timer is only the fallback for a missed hook. A shorter timer means more registry requests.

### Rollback After Automatic Update

`pantry-update.sh` records the image digest of each running service before `docker compose pull`. If the pull does not change those image ids, it stops, unless `/opt/pantry/.update-pending` is still there from a run that died after recreating containers. In that case the next run checks health against the revision label of the digest named in that file, then checks containers whose image changed, and rolls back if either check fails. An unchanged container that is down is a warning, not a rollback. A newer image pulled in the meantime is not the commit that check expects. If that pending digest is no longer on the machine, the resume check is skipped with a warning and nothing is rolled back. Service images are read from `docker compose config`. python3 is not required.

If an image changed, the script reads `org.opencontainers.image.revision` before recreating anything. An image without that label is not started. It then recreates the stack and polls `http://127.0.0.1:$HOST_PORT/health` and `http://127.0.0.1:$HOST_PORT/api/build` until `commit` equals that label (90 seconds, or `PANTRY_UPDATE_HEALTH_TIMEOUT`). The deadline uses `/proc/uptime`, so an NTP step after boot does not cut the wait short. While `/health` reports `migrating` (the server is listening, migrations are still running), the wait continues up to 10 minutes (`PANTRY_UPDATE_MIGRATION_TIMEOUT`). The public hostname is not used for the LAN check: `/health` there asks for the household password. When the public proxy is part of the update, the script also requests `https://$PUBLIC_HOST/api/build` through Caddy. In tunnel mode it requests `http://$PUBLIC_HOST/api/build` with `--resolve $PUBLIC_HOST:80:10.77.77.3`, which sends the hostname Caddyfile.tunnel actually serves to caddy-tunnel's listener on the `cf-tunnel` network, with a 5 second limit. That check is also made on the old stack before recreate. The probe is skipped only when the old stack was unreachable too, meaning curl exited 6, 7, 28, or 52. The exit code after recreate is the one that decides. A refusal or a hang after a reachable old stack fails the deploy and names that curl exit code. Curl 56 on the old stack is not unreachable: a healthy new listener still passes. The response commit has to match the new pantry image. Cloudflare still reaches Caddy as `http://caddy:80` inside the network. It gives up early if the pantry container has exited, is restarting, or its restart count rises. A failed `docker compose up` does not then wait out the health timeout, and that exit code is not itself a rollback. If every service whose image changed is healthy, the new images stay and the log warns. Before recreate, the script records each container's status, restart count, container id, and `State.StartedAt`. Afterwards only a service whose image changed can fail the deploy. An unchanged grocy-sync or cloudflared that is down or restarting, even one that was healthy before this recreate, logs a journal warning (`<4>`) and does not roll the release back or reject a digest. A down one is listed in `/opt/pantry/.update-sidecar-warn`. The next run checks that list even when the images did not change, warns again while the container is down, and removes the line once it is running. An unchanged proxy is different, because the probe reaches pantry through it. The script checks that proxy on the old stack first. In tunnel mode the old stack counts as passed only when the response is 2xx and its commit is the pantry revision that was already running. A failed probe is tried once more after `PANTRY_UPDATE_PROBE_RETRY_SECONDS` (default 3, never more than 15). Pantry is rejected only when that retry still fails, pantry's image changed, and the same proxy container is still running: same container id, same RestartCount, and the same `State.StartedAt`. A new container id, a higher restart count, or a later StartedAt is the proxy's failure (Compose before 2.35 can restart Caddy without changing the id or the restart count), so the script warns and does not reject pantry. The proxy stays in `.update-sidecar-warn` until a later probe passes, including while the container is still running. That later check, including a minute when no image changed, is the same probe a deploy makes. In tunnel mode the commit has to match the pantry image already recorded for this run, so a healthy listener clears the line. If the proxy was already failing, it only warns. Changed services are checked again after `PANTRY_UPDATE_SETTLE_SECONDS` (default 12, never more than 15). A restart in that window fails the deploy for a changed image. An unchanged container that restarts on its own, such as cloudflared, does not.

On timeout, a changed service that fails its check, or a commit that does not match, the script starts the previous digest again (`docker compose up -d --pull never` with a one-shot override), checks that image's health, and points the local image tag compose uses, such as `ghcr.io/rhionin/pantry:latest`, back at that previous digest. The same retag is applied to any other service whose digest changed. A `docker compose up` that fails while every changed service is healthy does not roll back. If the previous image starts but does not become healthy, the log says `BOTH IMAGES DOWN`. If no previous digest was recorded, the new image is left in place and that banner is not printed. A later manual `docker compose up -d` or `sudo ./setup.sh` then starts the previous image instead of the rejected one. The script exits non-zero. It does not change `.env` or `PANTRY_IMAGE_TAG`. The next timer run still pulls. A new remote digest is deployed. A failure pinned on one changed service rejects that digest. A pantry digest that already passed `/health` and `/api/build` is not rejected when a changed sidecar is what failed. It is rejected when an unchanged proxy answered before recreate, the same container is still running (same id, same RestartCount, same StartedAt), and the probe still fails after one retry. An unchanged proxy that was already failing, one whose container was replaced or restarted, and an unchanged grocy-sync or cloudflared that is down, do not reject a digest. A proxy listed in `.update-sidecar-warn` stays there until its probe passes. Every changed digest is rejected only when pantry itself is unverified, or when no failing changed service can be named. Lines in `/opt/pantry/.update-rejected-digest` are `service<TAB>digest`. That file is not the setup last-known-good sha. A later run retags each rejected service back to its previous digest and still deploys any other service that was not rejected. The first skip of a digest is a journal warning (`<4>`, priority warning). Later minutes log `skipping rejected <digest>` and do not warn again. Removing a line is how you retry that exact digest, and it also drops that digest from the warning mark so the next skip warns again. Logs from the rejected pantry container are saved to `/opt/pantry/update-rejected-pantry.log` before the rollback removes the container.

An image rollback cannot undo a migration. The migration runner is not transactional. If the new image migrated the database before it failed, the previous image is running against that schema. Automatic pre-migration snapshots, when one was taken, are files named `pantry-pre-*.db` in `/data` on the `pantry-data` volume. The rollback banner names a path only when the rejected container's log says this deploy wrote that snapshot. An "already exists" line is an older file and is not named. If this deploy did not write one, the banner says `no new snapshot found`. Restoring one over `/data/pantry.db` is manual. The update script does not restore the database. The deploy is skipped when `/opt/pantry`, `/tmp`, or the Docker data directory has less than 64 MiB free (`PANTRY_UPDATE_MIN_FREE_KIB`).

If an automatic update causes issues and you want to pin a commit yourself:

1. Disable automatic updates. The path unit has to stop too, or the next hook pulls `latest` again:
   ```bash
   sudo systemctl disable --now pantry-update.timer pantry-update.path
   ```

2. Pin to last known good version in `.env`:
   ```bash
   PANTRY_IMAGE_TAG=<last-good-commit-sha>
   ```

3. Apply rollback:
   ```bash
   cd /opt/pantry
   sudo docker compose up -d
   ```

4. Verify health:
   ```bash
   curl http://localhost:8080/health
   ```

## Authentication (Private Repository)

Currently, `ghcr.io/rhionin/pantry` is public and requires no authentication.

If the repository becomes private, you'll need to authenticate:

```bash
# Create a GitHub personal access token with 'read:packages' permission
# Then login on the Pi:
sudo docker login ghcr.io
# Username: your-github-username
# Password: your-personal-access-token
```

The credentials are stored in root's Docker config, which is the identity the update service runs as.

## Issue webhook

`.github/workflows/issue-webhook.yml` tells the Stockbot bot when a GitHub issue is opened, reopened, or assigned. These are Actions secrets on the repository (Settings → Secrets and variables → Actions). They are not `/opt/pantry/.env` values.

- `PANTRY_WEBHOOK_URL` — HTTPS endpoint that receives the POST.
- `PANTRY_WEBHOOK_KEY` — bearer token. The workflow sends it as `Authorization: Bearer <key>` and does not print it.

If either secret is empty, the run logs a notice and exits successfully. Otherwise it POSTs JSON built with `jq` from the event payload: `repo`, `action`, `issue_number`, `title`, `html_url`, `author_login`, `assignee_login` (the user just assigned, otherwise the issue's assignee, or null), and `created_at`. Runs for the same issue number share a concurrency group so a burst of events does not pile up. The workflow grants the GitHub token no permissions.

## Grocy sync

Stockbot can copy itself into [Grocy](https://grocy.info/) on the same Pi. Stockbot stays the source of truth. Grocy being down does not stop Stockbot, and Stockbot being down does not stop Grocy. The copy is a separate container, `pantry-grocy-sync`, and it stays off until the three values below are set.

Grocy itself is not in this repository. On this Pi it is the linuxserver image, container name `grocy`, started from `/home/pantrybot/grocy`, and attached to the Docker network `pantry_cf-tunnel`. Other containers reach it at `http://grocy:80`.

### Turn it on

1. In Grocy, open the user menu and create an API key. That key is not a Stockbot password.
2. On the Pi, edit `/opt/pantry/.env`. This file is never committed. Leave the values empty in git.

```bash
openssl rand -hex 32
```

Paste the output into `/opt/pantry/.env` with the Grocy URL and the API key. Docker Compose does not run shell substitutions in this file, so the token is the hex string itself.

```
GROCY_URL=http://grocy:80
GROCY_API_KEY=paste-the-key-from-the-grocy-screen
PANTRY_SYNC_TOKEN=paste-the-hex-from-openssl
GROCY_SYNC_INTERVAL=5m
TZ=America/Denver
```

`TZ` must be the same zone the Grocy container uses, or a stock-in late in the evening can land on the previous day in Grocy. The sidecar image has the zone database; the zone name is enough.

3. Recreate Stockbot so it loads the token, and start the sidecar. From `/opt/pantry`, after `sudo ./setup.sh` has copied `docker-compose.grocy.yml`:

```bash
cd /opt/pantry
sudo ./setup.sh
sudo docker compose -f docker-compose.yml -f docker-compose.grocy.yml --profile grocy up -d grocy-sync
sudo docker logs pantry-grocy-sync --tail=40
```

`sudo ./setup.sh` also starts the sidecar once all three values are in `/opt/pantry/.env`. A plain `docker compose up` does not. The update timer uses the same rule, so later image pulls recreate `pantry-grocy-sync` when the values stay set.

The sidecar is limited to 64 MB. It has no published ports. It joins the Compose project network (hostname `pantry`) and the existing network `pantry_cf-tunnel` (hostname `grocy`). That second network only hands out dynamic addresses from `10.77.77.4/30`, and Grocy already uses one. If `up` says the pool is exhausted, another container is holding the remaining address. Do not pin this service to `10.77.77.2` or `10.77.77.3`.

The first reconcile creates a Grocy location named Pantry, a quantity unit named Piece, products, barcodes, current stock, and a shopping list named Pantry. Grocy's own default shopping list is left alone. After that it repeats every five minutes, and sooner when Stockbot's event stream reports a change.

Grocy product names are unique regardless of case. A group that shares a name with a product is the parent `Name (group)`. Products that share a name are told apart with a brand, a package size, or a short Stockbot id. The original name stays in the `pantry_name` userfield. Existing Grocy products are matched by that `pantry_id` userfield and renamed in place.

### Past uses, once

New uses are written through Grocy's API when they happen. Grocy's API cannot backdate a consume, so older Stockbot history is a separate command. It is not part of the five-minute sync. It writes one row per Stockbot stock move into Grocy's `stock_log` with the real date, after copying `grocy.db`.

Stop Grocy first. The database is `data/grocy.db` inside the directory the linuxserver image mounts at `/config`. On this Pi that compose file is `/home/pantrybot/grocy`; confirm the host path before you mount it. The mount has to be writable: the command writes a `grocy.db.bak-<timestamp>` beside the live file, then the journal rows.

The sidecar image runs as uid 65532. Grocy's files on this Pi are owned by uid 1001, so backfill has to run as that user. Without `--user 1001:1001` the command stops before it copies or writes anything.

```bash
sudo docker stop grocy
cd /opt/pantry
sudo docker compose -f docker-compose.yml -f docker-compose.grocy.yml --profile grocy run --rm --no-deps --user 1001:1001 \
  -v /home/pantrybot/grocy/config:/grocy \
  grocy-sync backfill --db /grocy/data/grocy.db --confirm
sudo docker start grocy
```

Run a reconcile (the sidecar's normal start) before the first backfill, so Grocy already has the products and the Pantry location. Running it again updates rows that changed and marks removed moves undone. It does not change the on-hand `stock` table, so current counts stay with the API sync.

If grocy.db-wal or grocy.db-shm is present, or the database is locked, backfill stops before it copies or writes anything. Stop Grocy and run it again. If the backup succeeds and a later write fails, the log prints the restore steps. They are:

```bash
sudo docker stop grocy
# Move the live grocy.db aside, copy grocy.db.bak-<timestamp> to grocy.db,
# and delete grocy.db-wal and grocy.db-shm if those files exist.
sudo docker start grocy
```

### What didn't map

- Group rules, pinned products, and "don't restock" are notes on the Grocy product. They do not change how Grocy shops.
- A time window or a grams/milliliters target is not a Grocy minimum stock. Minimum stock is set only where Stockbot has a fixed item count (`target_quantity`). Parent products roll that count up from their members.
- A barcode override replaces the global barcode. Grocy stores one product per barcode and does not keep the source.
- Package size stays a note and a `pantry_size` userfield. Grocy still counts pieces. There is no unit conversion.
- Prices and deals are not sent. Brand is not its own Grocy field.
- Product pictures are not uploaded.
- Scans waiting for approval, suggestions, and the staged cart are not copied.
- Grocy recipes, chores, batteries, equipment, and locations other than Pantry are left alone. So is Grocy's default shopping list.
- A consume written through the API is stamped when Grocy receives it. Only the one-time backfill keeps the original past date.
- Correcting a backfilled journal row is applied the next time you run `backfill`. Correcting current stock undoes the matching Grocy booking and posts it again on the next sync.
- One household (`user-1`). Parent products are a single level. When a product is in a Stockbot group, that group's name is the Grocy product group. The Stockbot category is a product group only for products that are not in a Stockbot group; otherwise the category is a note.

## Troubleshooting

### Public website doesn't load

See [Public Internet access](#public-internet-access). On the certificate path, the usual causes are the Dynu A record (or whichever DNS host is authoritative) not pointing at this Pi yet, or router ports 80 and 443 not forwarded. A hang that happens only on home Wi-Fi, while cellular loads the site, is [NAT hairpin](#home-wi-fi-hangs-on-the-public-name), not a down server. In tunnel mode, both networks use Cloudflare; logs for that path are `sudo docker compose -f docker-compose.yml -f docker-compose.tunnel.yml --profile tunnel logs --tail=80 cloudflared`. From `/opt/pantry`:

```bash
sudo docker compose --profile public logs --tail=80 caddy
```

### Container Won't Start

Start with the full-chain diagnostic:
```bash
sudo ./setup.sh status
```

Check logs:
```bash
sudo docker compose logs pantry
```

Common issues:
- Database permissions: Ensure the `/data` volume is writable by uid 65532
- Port conflict: Another service using port 8080 (change `HOST_PORT` in `.env`)
- Image pull failure: Check network connectivity and authentication

Note the image is distroless, so `docker exec pantry sh` does not work — use logs and `/health` instead.

### Scanner Not Working

1. **Run the diagnostic first — it names the broken link:**
   ```bash
   sudo ./setup.sh status
   ```

2. **Upgrading from a pre-hot-plug version? Check `SCANNER_DEVICE`.**
   ```bash
   grep '^SCANNER_DEVICE=' /opt/pantry/.env
   ```
   An older `.env` pinned `SCANNER_DEVICE=/dev/pantry-scanner`, and the container can no longer open that path — the symlink now lives at `/dev/input/pantry-scanner`. Running `sudo ./setup.sh` migrates this obsolete default automatically; after it runs, the value should be `/dev/input/pantry-scanner`. (A custom path you set on purpose is left untouched.) Symptom of the stale value: logs show `failed to open device /dev/pantry-scanner: no such file or directory` and `status` reports the old path.

3. **Check scanner status via `/health`:**
   ```bash
   curl http://localhost:8080/health
   ```
   Then use the `lastError` table in [Headless Scanner Input → Troubleshooting](#troubleshooting) to tell `ENOENT` (rule didn't match), `EACCES` (wrong group), and `EPERM` (cgroup denied) apart — they have different fixes. Remember that `connected: false` with **no** error and no scanner attached is normal and supported.

4. **Verify `/dev/input/pantry-scanner` exists:**
   ```bash
   ls -l /dev/input/pantry-scanner
   ```
   If it is missing while the scanner is attached — and especially if the scanner's keystrokes are appearing in the Pi's terminal — the udev rule is not matching. The scanner echoing to the console is the tell: the rule that would grant the node to GID 65532 and create the symlink never fired. Regenerate it with `sudo ./setup.sh rule` (it matches USB and Bluetooth HID scanners on the input parent's vendor/product IDs) and confirm with `ls -l /dev/input/pantry-scanner`.

5. **Check the container's mounts** (there is no longer a `Devices` array — the scanner is a directory bind mount now):
   ```bash
   sudo docker inspect -f '{{json .Mounts}}' pantry
   ```
   The `Mounts` array should include the `/dev/input` bind mount.

6. **Verify the udev rule matches your device:**
   ```bash
   lsusb                              # USB
   cat /proc/bus/input/devices        # any input device
   ```
   Ensure `ATTRS{id/vendor}`/`ATTRS{id/product}` in `/etc/udev/rules.d/99-pantry-scanner.rules` match your scanner (`ATTRS{name}` only when those IDs are absent), then re-run `sudo ./setup.sh rule`.

### Updates Failing

Check update service logs:
```bash
sudo journalctl -u pantry-update -f
```

Common issues:
- Network connectivity during `docker compose pull`
- Docker daemon not running
- Incorrect file paths in systemd unit

### Permission Issues

If using host bind mounts instead of named volumes:
```bash
sudo chown -R 65532:65532 /opt/pantry/data
```

### Configuration Not Applied

Ensure `.env` file is in the same directory as `docker-compose.yml`:
```bash
ls -la /opt/pantry/.env
cd /opt/pantry
sudo docker compose up -d
```

## Support

For issues and support:
- Check the [Stockbot repository](https://github.com/Rhionin/stockbot) for documentation and issues
- Review container logs: `sudo docker compose logs pantry`
- Check system resources: `free -h && df -h`
- Verify network connectivity to GitHub Container Registry

## Development vs Production

This deployment guide covers the production container setup. For local development:

- See `frontend/README.md` for the development workflow
- Development uses separate Vite dev server + Go API server
- This deployment serves the compiled frontend embedded in the Go binary

The production container serves both the frontend and API on a single port, while development uses two separate processes on different ports.
