# Stockbot hosted (multi-household): cost arithmetic

Region: **us-east-1 (N. Virginia)**. All prices accessed **2026-10-22**. Every unit price comes from the AWS Price List bulk API (`https://pricing.us-east-1.amazonaws.com/offers/v1.0/aws/<ServiceCode>/current/us-east-1/index.json`). This is the same data the Pricing API `get_pricing` returns. I cross-checked against the public pricing page listed for each one. "PL" below means price-list offer file (service code, publication date).

Costs are USD per 730-hour month. They **exclude** the new-account Free Tier credits ($100–200) and the 12-month-only free tiers (API Gateway 1M calls, Aurora Data API 1M requests). They **include** the free tiers that never expire (Lambda, DynamoDB 25 GB storage, DSQL 100k DPU + 1 GB, Cognito 10k MAU, CloudWatch basics, EventBridge Scheduler 14M). If a number is my estimate rather than a published price, it is marked **(estimate)**.

## 1. Unit prices used

| Item | Price | Source (PL service code, publication date) | Public page |
|---|---|---|---|
| Lambda arm64 requests | $0.20 / 1M | AWSLambda, 2026-10-22T18:39Z (`Request-ARM`) | https://aws.amazon.com/lambda/pricing/ |
| Lambda arm64 duration | $0.0000133334 / GB-s (tier 1) | AWSLambda, 2026-10-22 (`Lambda-GB-Second-ARM`) | same |
| Lambda free tier | 1M req + 400,000 GB-s / month (does not expire) | Lambda pricing page | same |
| Lambda Function URL | no extra charge (billed as Lambda requests/duration) | Lambda pricing page | same |
| API Gateway HTTP API | $1.00 / 1M (first 300M) | AmazonApiGateway, 2026-10-13 (`USE1-ApiGatewayHttpRequest`) | https://aws.amazon.com/api-gateway/pricing/ |
| Aurora Serverless v2 PG (Standard) | $0.12 / ACU-hr | AmazonRDS, 2026-10-20 (`Aurora:ServerlessV2Usage`, Aurora PostgreSQL) | https://aws.amazon.com/rds/aurora/pricing/ |
| Aurora Serverless v2 PG (I/O-Optimized) | $0.16 / ACU-hr (the PL says 0.16; a page example shows 0.156) | AmazonRDS, 2026-10-20 | same |
| Aurora storage (Standard) | $0.10 / GB-mo | AmazonRDS (`Aurora:StorageUsage`) | same |
| Aurora I/O (Standard) | $0.20 / 1M I/O | AmazonRDS (`Aurora:StorageIOUsage`) | same |
| Aurora backup beyond 100% of cluster size | $0.021 / GB-mo | AmazonRDS (`Aurora:BackupUsage`) | same |
| RDS Data API | $0.35 / 1M requests (per 32 KB); 1M/mo free for the first year only | Aurora pricing page | same |
| RDS Proxy on Serverless v2 | $0.015 / ACU-hr (and Proxy **prevents auto-pause**) | AmazonRDS (`USE1-RDS:Proxy-ASv2-Usage`) | same |
| Aurora DSQL DPU | $0.000008 / DPU ($8 / 1M) | AuroraDSQL, 2026-09-11 (`USE1-DSQL-DistributedProcessingUnits`) | https://aws.amazon.com/rds/aurora/dsql/pricing/ |
| Aurora DSQL storage | $0.33 / GB-mo | AuroraDSQL (`USE1-DSQL-Storage-ByteHrs`) | same |
| Aurora DSQL free tier | 100,000 DPU + 1 GB storage / month (`Global-DSQL-*`, $0) | AuroraDSQL | same |
| DSQL backup (AWS Backup warm) | $0.10 / GB-mo | AWSBackup, 2026-10-14 (`USE1-WarmStorage-ByteHrs-DSQL`) | https://aws.amazon.com/backup/pricing/ |
| DynamoDB on-demand writes | $0.625 / 1M WRU | AmazonDynamoDB, 2026-09-11 | https://aws.amazon.com/dynamodb/pricing/on-demand/ |
| DynamoDB on-demand reads | $0.125 / 1M RRU | same | same |
| DynamoDB storage | first 25 GB free, then $0.25 / GB-mo | same | same |
| DynamoDB PITR | $0.20 / GB-mo | same | same |
| RDS db.t4g.micro PostgreSQL Single-AZ | $0.016 / hr (Multi-AZ $0.032) | AWS pricing JSON `rds-postgresql-ondemand` (b0.p.awsstatic.com), 2026-10-22 | https://aws.amazon.com/rds/postgresql/pricing/ |
| RDS gp3 storage | $0.115 / GB-mo (PG minimum 20 GB) | AmazonRDS (`RDS:GP3-Storage`) | same |
| EC2 t4g.nano / t4g.micro | $0.0042 / $0.0084 per hr | AWS pricing JSON `ec2-ondemand` Linux, us-east-1 | https://aws.amazon.com/ec2/pricing/on-demand/ |
| Lightsail Linux 1 GB, IPv4 included | $7 / mo (0.5 GB: $5; IPv6-only 1 GB: $5) | Lightsail pricing page | https://aws.amazon.com/lightsail/pricing/ |
| Fargate Linux/ARM | $0.0000089944 / vCPU-s = $0.03238 / vCPU-hr; $0.0000009889 / GB-s = $0.00356 / GB-hr | Fargate page, us-east-1 example | https://aws.amazon.com/fargate/pricing/ |
| ALB | $0.0225 / hr + $0.008 / LCU-hr | ELB page, us-east-1 example | https://aws.amazon.com/elasticloadbalancing/pricing/ |
| Public IPv4 (in-use or idle) | $0.005 / hr = $3.65 / mo | VPC page | https://aws.amazon.com/vpc/pricing/ |
| NAT gateway | $0.045 / hr = $32.85 / mo + $0.045 / GB | VPC page | same |
| Interface VPC endpoint | $0.01 / AZ-hr = $7.30 / AZ-mo (+$0.01 / GB) | API GW page, PrivateLink example | https://aws.amazon.com/privatelink/pricing/ |
| CloudFront flat-rate **Free** plan | $0 / mo: 1M requests, 100 GB, DNS, TLS cert, WAF (5 rules), 5 GB S3 credit | CloudFront page | https://aws.amazon.com/cloudfront/pricing/ |
| S3 Standard | $0.023 / GB-mo; PUT $0.005 / 1k; GET $0.0004 / 1k | AmazonS3, 2026-10-15 | https://aws.amazon.com/s3/pricing/ |
| Route 53 hosted zone | $0.50 / zone-mo; alias queries to CloudFront free | Route 53 page | https://aws.amazon.com/route53/pricing/ |
| Cognito Essentials / Lite | 10,000 MAU free per month (does not expire); then Essentials $0.015 / MAU, Lite $0.0055 / MAU | AmazonCognito PL 2026-10-20; Cognito page | https://aws.amazon.com/cognito/pricing/ |
| Cognito default email sender | 50 emails/day per account, not adjustable, so SES is required (quota, not a price) | Cognito quotas doc | https://docs.aws.amazon.com/cognito/latest/developerguide/quotas.html |
| SES outbound | à la carte $0.10 / 1,000; Essentials plan (the default for new accounts since 2026-07-21) $0.16 / 1,000 | SES page | https://aws.amazon.com/ses/pricing/ |
| Secrets Manager | $0.40 / secret-mo + $0.05 / 10k API calls | AWSSecretsManager, 2026-09-11 | https://aws.amazon.com/secrets-manager/pricing/ |
| SSM Parameter Store Standard (SecureString) | no charge for standard parameters / standard throughput (verify on page) | — | https://aws.amazon.com/systems-manager/pricing/ |
| KMS customer-managed key | $1 / key-mo; $0.03 / 10k requests; 20k requests/mo free | awskms, 2026-09-11 | https://aws.amazon.com/kms/pricing/ |
| CloudWatch Logs | $0.50 / GB ingested (Standard); $0.03 / GB-mo stored | AmazonCloudWatch, 2026-10-07 | https://aws.amazon.com/cloudwatch/pricing/ |
| CloudWatch alarm | $0.10 / standard alarm metric-mo | AmazonCloudWatch | same |
| CloudWatch free tier | 5 GB logs, 10 alarms, 10 custom metrics (per the free-tier section; verify) | CloudWatch page | same |
| EventBridge Scheduler | first 14M scheduled invocations / mo free, then $1 / 1M | AWSEvents, 2026-09-24 (`USE1-ScheduledInvocation`) | https://aws.amazon.com/eventbridge/pricing/ |
| AWS Budgets | budget monitoring and notifications free; first 2 action-enabled budgets free | Budgets page | https://aws.amazon.com/aws-cost-management/aws-budgets/pricing/ |

## 2. Workload assumptions (estimate)

- 3 hosted households, ~8 people, ~5 phones. CJ's house stays on the Pi; one-time import later.
- **Low-traffic month**:
  - ~30 scans/day in total.
  - ~100 page loads/day × ~5 API calls = 500 calls/day.
  - Other visible screens poll every 10 s, and polling stops in the background: ~2 h/day of visible tabs in total = 720 calls/day.
  - Base workload (scans, page loads, those 10 s polls): ≈ **1,250 requests/day ≈ 37,500 requests/month**.
- Lambda, base workload: 512 MB × 100 ms average = 0.05 GB-s per request → 37,500 × 0.05 = **1,875 GB-s/month**.
- **Scan Queue polling (PM decision 2026-10-22, estimate):** 3 households with the queue visible 1 h/day each. Polling every 2 s is 1,800 requests/h, so 3 × 1,800 × 30 ≈ **162,000 requests/month**.
  - Lambda: 162,000 × 0.05 GB-s = **8,100 GB-s**. Combined with the base (199,500 requests, 9,975 GB-s) this stays inside the never-expiring free tier (1M requests, 400,000 GB-s), so **$0**.
  - DSQL: at the conservative 0.5 DPU/request estimate, about **81,000 DPU**. Plus the base 18,750 that is about **99,750**, right at the 100k free tier, so about **$0**.
  - Scale: at 10 households with the same usage, about 540,000 polls is 270,000 DPU, adding about **$1.40/month** at $8 per 1M DPU.
  - Aurora Serverless v2: frequent polling keeps the cluster awake while the queue is open, which strengthens the case against option A.
- DB: **0.2 GB** of data (estimate; household pantries are a few MB each). Base workload: ~3 SQL statements per request → ~112,500 statements/month. Scan Queue polls are a single indexed read; their DPU and read charges are in the bullets above and in §4.
- Email (verify, invite, reset): ~50/month.
- Logs: ~0.2 GB/month ingested.
- **Idle month**: zero user requests. Only scheduled jobs run (a daily product refresh, ~30 invocations/month).
- No ESP32 relays yet. A relay heartbeating every 60 s adds 43,200 requests/month/device; see the Aurora note below.

## 3. Shared platform costs (the same for every option)

| Item | Arithmetic | $/mo |
|---|---|---|
| CloudFront Free plan (SPA + API front door) | flat | 0.00 |
| S3 for SPA assets + exports (≤ 0.1 GB, inside the 5 GB plan credit) | 0.1 × 0.023 | ~0.00 |
| Route 53 hosted zone for the new hosted origin (skip it if DNS stays on Cloudflare) | 1 × 0.50 | 0.50 |
| Cognito Essentials, < 10k MAU | free tier | 0.00 |
| SES, 50 emails | 50 / 1000 × 0.16 | 0.01 |
| KMS CMK for envelope-encrypting provider OAuth tokens | 1 × 1.00 | 1.00 |
| Platform secrets in SSM Parameter Store SecureString (Kroger creds, VAPID private key, Sentry DSN, OFF creds, retailer key) | standard params | 0.00 |
| (alternative: those 5 in Secrets Manager) | 5 × 0.40 | (2.00) |
| CloudWatch Logs 0.2 GB, ~8 alarms | inside free tier | 0.00 |
| SNS email topic for operator alerts, AWS Budgets alarm | free / negligible | 0.00 |
| EventBridge Scheduler (push batches ~900/mo + daily jobs) | < 14M free | 0.00 |
| **Shared total** | | **≈ 1.51** |

## 4. Options (DB + compute) — monthly arithmetic

### A. Lambda (arm64, no VPC) + Aurora Serverless v2 PG, min 0 ACU (auto-pause) via **Data API**
The Data API lets Lambda stay outside the VPC, so there is no NAT. The cluster itself sits in a VPC; the default VPC is fine, with no public IP.
- Storage: 0.2 GB × 0.10 = **$0.02**
- I/O: ~0.5M × 0.20/1M = **$0.10** (estimate)
- Data API secret (required by the Data API): 1 × 0.40 = **$0.40**
- Data API requests: 112,500 × 0.35/1M = **$0.04**
- ACU, idle month: 0. Maintenance wakes (≥ 20 min each) and ~30 scheduled jobs that wake the DB: ~30 wakes × (5 min job + 5 min pause delay) ≈ 5 h × 0.5 ACU × 0.12 = **$0.30**
- ACU, low-traffic month: ~15 active windows/day × (~5 min active + 300 s minimum before pausing) = 150 min/day = 2.5 h/day × 30 = 75 h × 0.5 ACU × 0.12 = **$4.50** (estimate; this assumes the running floor is 0.5 ACU)
- Worst case if anything keeps a connection or makes regular calls (for example a relay heartbeat or polling that reaches the DB): 730 × 0.5 × 0.12 = **$43.80**
- Scan Queue polling (estimate): 2 s polls while the queue is open (3 households × 1 h/day) keep the cluster awake for those hours. Any request resumes it, and the 300 s pause timer never elapses during that polling, so this strengthens the case against option A. The ~$6.57 figure is the earlier active-window model; it is not a solid hour of 2 s polling per household.
- Lambda: base 1,875 GB-s plus Scan Queue 8,100 GB-s = 9,975 GB-s, and 199,500 requests, inside the free tier = **$0.00** (without the free tier the base alone was 0.0075 + 0.025 = $0.03)
- **Idle** = 0.02 + 0.40 + 0.30 + shared 1.51 ≈ **$2.23**. **Low traffic** = 0.02 + 0.10 + 0.40 + 0.04 + 4.50 + 1.51 ≈ **$6.57**. The Scan Queue polls add no Lambda charge. Risk: $45+, and the queue-open polling pushes toward that case.

### B. Lambda (arm64, no VPC) + DynamoDB on-demand
- Writes: ~500 WRU/day × 30 = 15,000 × 0.625/1M = **$0.01**
- Reads, base workload: 37,500 × ~10 RRU = 375,000 × 0.125/1M = **$0.05**
- Scan Queue polls (estimate): 162,000 × ~10 RRU = 1,620,000 × 0.125/1M = **$0.20**
- Storage: 0.2 GB, inside the 25 GB free tier = **$0.00**. PITR: 0.2 × 0.20 = **$0.04**
- Lambda: base plus Scan Queue polling stays inside the free tier = **$0.00**
- **Idle** ≈ 0.04 + 1.51 = **$1.55**. **Low traffic** ≈ 0.01 + 0.05 + 0.20 + 0.04 + 1.51 = **$1.81**.

### C. Lambda (arm64, no VPC) + **Aurora DSQL** (single-Region)
- DPU (estimate): AWS's benchmark is ~700,000 TPC-C transactions per 100k DPU, about 0.14 DPU per transaction. I assume a conservative **0.5 DPU per request**; measure it with CloudWatch `TotalDPU` (namespace `AWS/AuroraDSQL`) in dev. Base workload: 37,500 × 0.5 = 18,750 DPU. Scan Queue polling: 162,000 × 0.5 = 81,000 DPU. Combined **99,750 DPU**, right at the 100k free tier, so about **$0.00**.
- Storage: 0.2 GB, inside the 1 GB free tier = **$0.00**
- AWS Backup daily, 14-day retention (assuming full copies): 14 × 0.2 GB = 2.8 GB-mo × 0.10 = **$0.28**
- No secret is needed, because auth uses IAM tokens. No VPC, no NAT, no IPv4.
- Lambda: base 1,875 GB-s plus Scan Queue 8,100 GB-s = 9,975 GB-s, and 199,500 requests, inside the never-expiring free tier = **$0.00**
- **Idle** ≈ 0.28 + 1.51 = **$1.79**. **Low traffic** ≈ **$1.79** (the polls add no DSQL or Lambda charge at 3 households). Scale (estimate): 10 households with the queue visible 1 h/day each is about 540,000 polls = 270,000 DPU, adding about **$1.40/month** at $8 per 1M DPU (270,000 − 100,000 free ≈ 170,000 × $0.000008 ≈ $1.36).

### D. Lightsail 1 GB ($7, IPv4 included) running today's Go server + SQLite + Litestream → S3
- Instance: **$7.00**
- S3 replica: 0.5 GB × 0.023 = **$0.01**; ~20k PUTs × 0.005/1k = **$0.10** (estimate)
- The shared CloudFront and Route 53 lines are optional here; I keep them for an apples-to-apples comparison.
- **Idle** ≈ 7.01 + 1.51 = **$8.52**. **Low traffic** ≈ **$8.62**. (EC2 t4g.micro equivalent: 0.0084 × 730 = 6.13 + IPv4 3.65 + 8 GB gp3 ≈ 0.64 = $10.42.)
- Note: this is **not a serverless DB**, which conflicts with CJ decision 3. It is included as the low-effort baseline.

### E. Fargate (ARM, 0.25 vCPU / 0.5 GB) + ALB + RDS db.t4g.micro PG (the expensive end)
- ALB: 0.0225 × 730 = 16.43; ALB public IPv4 for 2 AZs: 2 × 3.65 = 7.30; LCU ≈ 0.01 × 730 × 0.008 = 0.06
- Fargate: vCPU 0.25 × 0.03238 × 730 = 5.91; memory 0.5 × 0.00356 × 730 = 1.30; task public IPv4 3.65 (or NAT instead: +32.85)
- RDS: 0.016 × 730 = 11.68; gp3 20 GB × 0.115 = 2.30
- **Idle = low traffic** ≈ 48.63 + 1.51 = **$50.14**

## 5. Summary table

| Option | Idle month | Low-traffic month | Fixed-cost traps | Code rework (opinion) |
|---|---|---|---|---|
| A. Lambda + Aurora SLv2 (0 ACU) + Data API | ~$2.23 | ~$6.57 (up to ~$45 if it never pauses) | Data API secret; 2 s Scan Queue polling keeps it awake while the queue is open; RDS Proxy disables pause | Postgres port + Lambda statelessness; 15–30 s first-request resume |
| B. Lambda + DynamoDB | ~$1.55 | ~$1.81 | none | **Full data-layer rewrite** + Lambda statelessness; JSON/CSV export is a translator |
| **C. Lambda + Aurora DSQL** | **~$1.79** | **~$1.79** | backups only | Postgres port with DSQL limits (OCC retries, 3,000-row transactions, no triggers or PL/pgSQL) + Lambda statelessness. Scan Queue polling is inside the 100k DPU free tier at 3 households (estimate) |
| D. Lightsail + SQLite + Litestream | ~$8.52 | ~$8.62 | instance always on | Smallest: multi-tenant work only; keeps SSE and timers |
| E. Fargate + ALB + RDS t4g.micro | ~$50.14 | ~$50.14 | ALB + IPv4 + always-on DB | Postgres port only |

Lambda statelessness rework (A, B, C) means replacing the in-memory state: the SSE broadcaster `/api/events` becomes polling (2 s while the Scan Queue is visible, 10 s on other visible screens, off in the background); the push-attention timers become EventBridge Scheduler one-time schedules; the scanner-mode idle timer becomes a stored expiry; the background product refresh becomes a scheduled Lambda; the telemetry registry and login rate limiter become DB rows. Rough estimate (opinion): about 1–2 weeks of one engineer. See [multi-tenant-saas.md](multi-tenant-saas.md).

## 6. Live updates: polling vs push

This section compares four live-update styles on the two serverless databases still in play: **C. Aurora DSQL** and **A. Aurora Serverless v2 (min 0 ACU) via Data API**.

It uses the same conventions as §1–§5: USD per 30-day month, us-east-1, the never-expiring free tiers included, and the 12-month free tiers excluded. Every usage figure is my **(estimate)**. Prices were accessed 2026-10-22. Style (a) is today's PM-decided design. At 3 households it reproduces §2 and §4C exactly: 199,500 requests and 99,750 DPU.

### 6.1 Unit prices added

| Item | Price | Service code, publication date (`usagetype`) | Pricing page |
|---|---|---|---|
| API Gateway WebSocket messages | $1.00 / 1M (first 1B), then $0.80 / 1M | AmazonApiGateway, 2026-10-13T00:30:44Z (`USE1-ApiGatewayMessage`) | https://aws.amazon.com/api-gateway/pricing/ |
| API Gateway WebSocket connection minutes | $0.25 / 1M minutes | AmazonApiGateway, 2026-10-13T00:30:44Z (`USE1-ApiGatewayMinute`) | same |
| AppSync Events operations | $1.00 / 1M operations | AWSAppSync, 2026-09-11T12:45:25Z (`USE1-EventAPI-Operation`) | https://aws.amazon.com/appsync/pricing/ |
| AppSync Events connection minutes | $0.08 / 1M minutes | AWSAppSync, 2026-09-11T12:45:25Z (`USE1-EventAPI-ConnectionDuration`) | same |
| CloudFront flat-rate plans | Free $0 (1M requests/mo), Pro $15/mo (10M requests/mo), no overage charges | CloudFront pricing page | https://aws.amazon.com/cloudfront/pricing/ |

The prices come from the AWS bulk Price List files for those service codes. The Pricing MCP `get_pricing` cross-check failed with "Unable to locate credentials".

Metering rules, from the pricing pages as of 2026-10-22:
- **API Gateway WebSocket:**
  - Messages are metered in 32 KB increments.
  - Control frames (ping/pong) are not metered.
  - Free tier: 1M messages and 750,000 connection minutes per month, for 12 months.
- **AppSync Events:**
  - Inbound publishes, outbound deliveries, event-handler invocations, connections, subscription requests and "ping requests" are all operations.
  - Real-time messages are metered per 5 KB.
  - Free tier: 250,000 real-time updates and 600,000 connection minutes per month, for 12 months.

### 6.2 Workload, per household per month (estimate)

The workload follows §2.

| Item | Value | Basis |
|---|---|---|
| Non-poll API requests | 5,300 | §2 base of 37,500 for 3 households, minus the 10 s polls |
| 10 s polls on other visible screens | 7,200 | §2: 720/day for 3 households, which is 40 visible min/day per household |
| 2 s Scan Queue polls | 54,000 | §2: 1 h/day × 1,800/h × 30 |
| Visible time | 100 min/day, in 4 sessions | 60 min on the Scan Queue + 40 min on other screens |
| Change events | 1,500 | ~30 scans + ~20 stock changes per day. A relay scan is one event. |
| Push fan-out | 1.5 open sessions per event, or 1.0 for (d) | The scanning phone is usually one of the receivers |
| Refetches under push | 1 per connect (120) + 1 per hint received | Hints are type + cursor only, < 1 KB, no item names |
| Static-asset requests | 1,200 | Through CloudFront |
| Lambda | 0.05 GB-s per request | §2 |
| DSQL | 0.5 DPU per request | §4C. Connection-registry rows cost 0.2 DPU. |
| Serverless v2, polling sessions | 25 min visible + 5 min pause delay = 30 min awake per session | Sessions are assumed not to overlap across households, which makes this an upper bound |
| Serverless v2, push sessions | ~10 min awake per session | Only loads, scans and refetches touch the DB |
| Serverless v2, scheduled jobs | + 5 h/month | §4A |

### 6.3 Arithmetic per style (per household per month unless noted)

**(a) Polling: 2 s on the Scan Queue, 10 s elsewhere (the current design)**
- Requests: 5,300 + 7,200 + 54,000 = 66,500.
  - 3 households: 199,500.
  - 10 households: 665,000. The Lambda free tier still covers this, so **$0**.
- DSQL: 33,250 DPU per household.
  - 3 households: 99,750 DPU, which is **$0.00**.
  - 10 households: 332,500 DPU. 232,500 billable × $8/1M = **$1.86**.
  - §2 quotes "+$1.40" for 10 households. That figure nets the free tier against the polls alone; counting base traffic as well gives $1.86 in total.
- Serverless v2:
  - 3 households: 3 × 120 sessions × 30 min = 180 h + 5 = 185 h × 0.5 ACU × $0.12 = **$11.10**, plus the fixed items.
  - 10 households: 605 h = **$36.30**, which is close to always on.
- CloudFront: 67,700 requests per household, so the Free plan's 1M fills at **~14 households**.

**(b) Push everywhere via API Gateway WebSocket**
- Messages ≈ 2,850 → $0.003:
  - 2,250 deliveries (1,500 × 1.5);
  - 360 app keepalives (3 per session; the idle timeout is 10 min and browsers can't send ping frames);
  - 240 connect/disconnect messages (counted conservatively).
- Connection minutes: 3,000 → $0.0008.
- Connection registry: DynamoDB on Serverless v2, so registry writes don't wake the cluster, at < $0.001. On DSQL it is a DSQL table at ~800 DPU.
- Lambda: 360 authorizer/`$connect`/`$disconnect` calls + 1,500 fan-outs. The keepalive route is a MOCK integration.
- Requests: 5,300 + 120 + 2,250 = 7,670.
- DSQL: ~4,000 DPU per household, so free until ~25 households.
- Serverless v2:
  - 3 households: 3 × 120 × 10 min = 60 h + 5 = 65 h = **$3.90**.
  - 10 households: 205 h = **$12.30**.

**(c) Push everywhere via AppSync Events**
- Operations: 120 connects + 120 subscribes + 120 authorizer evaluations + 1,500 publishes + 2,250 deliveries = **4,110** → $0.004.
  - Upper bound if the server's 60 s `ka` keep-alives are billed as "ping requests" (unverified): +3,000 → $0.007.
- Connection minutes: 3,000 → $0.0002.
- No registry.
- Requests: 7,670.
- DSQL: ~3,800 DPU per household. Serverless v2: the same hours as (b).

**(d) Hybrid: AppSync Events on the visible Scan Queue only, 10 s polling elsewhere**
- Operations: 360 + 1,500 + 1,500 = 3,360 (up to 5,160 with `ka`).
- Connection minutes: 1,800.
- Requests: 5,300 + 7,200 + 120 + 1,500 = 14,120.
- DSQL: ~7,100 DPU per household, so free until ~14 households.
- Serverless v2: the 10 s polls still keep the cluster awake for each whole session, so hours ≈ (a).

### 6.4 Summary, USD/month

Each figure is the option total: option C's or option A's own costs, plus the shared $1.51, plus the live-update cost. Lambda is $0 in every cell.

| Style | C. DSQL, 3 hh | C. DSQL, 10 hh | A. Serverless v2, 3 hh | A. Serverless v2, 10 hh | Serverless v2 awake h (3 / 10 hh) | CloudFront Free plan fills at |
|---|---|---|---|---|---|---|
| (a) Polling, 2 s queue / 10 s elsewhere (current) | **1.79** | **3.65** | 13.21 | 38.83 | 185 / 605 | ~14 hh |
| (b) Push everywhere, API GW WebSocket | 1.80 | 1.83 | 5.97 | 14.68 | 65 / 205 | ~112 hh |
| (c) Push everywhere, AppSync Events | 1.80 | 1.83 (≤ 1.86) | 5.97 | 14.69 | 65 / 205 | ~112 hh |
| (d) Hybrid: push on the Scan Queue, 10 s polls elsewhere | 1.80 | 1.83 | 13.17 | 38.70 | 185 / 605 | ~65 hh |

How the totals are built:
- **DSQL** = §4C's $1.79 (backups + shared) + DPU above the free tier + push charges.
- **Serverless v2** = shared $1.51 + storage + I/O (scaled per household) + the $0.40 Data API secret + Data API statements + awake hours × $0.06/h + push charges.
- The Serverless v2 polling figures replace §4A's "$6.57 low traffic" active-window model whenever the Scan Queue is open 1 h/day per household.

Reading the table:
- **On DSQL every style costs about $2/month or less.**
  - Polling's real costs are the CloudFront Free plan request quota and phone battery and data.
    - The quota fills at ~14 households. After that the Pro plan costs $15/month.
    - Push moves the quota limit to ~112 households.
  - Polling also leaves the DSQL free tier at about 3 households: it is at 99,750 DPU today.
- **On Serverless v2, polling is what costs money.** Any style that polls the DB from visible screens keeps the cluster awake for every session. Push connections never touch the DB, which cuts the awake hours by about two thirds.
- **API GW WebSocket and AppSync Events cost the same at this scale, to within a few cents.** Choose between them on build effort and security.
- **With the 12-month free tiers, both push services cost $0 in year one.**

`push_model.py` (kept beside the working docs, not committed) reproduces every number above. Its inputs are the assumptions in §6.2.
