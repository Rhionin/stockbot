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
  - Live-update polling while a tab is visible: one call every 10 s × ~2 h/day of visible tabs in total = 720 calls/day.
  - That totals ≈ **1,250 requests/day ≈ 37,500 requests/month**.
- Lambda: 512 MB × 100 ms average = 0.05 GB-s per request → 37,500 × 0.05 = **1,875 GB-s/month**.
- DB: **0.2 GB** of data (estimate; household pantries are a few MB each). ~3 SQL statements per request → ~112,500 statements/month.
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
- Lambda: 37,500 requests and 1,875 GB-s, inside the free tier = **$0.00** (without the free tier: 0.0075 + 0.025 = $0.03)
- **Idle** = 0.02 + 0.40 + 0.30 + shared 1.51 ≈ **$2.23**. **Low traffic** = 0.02 + 0.10 + 0.40 + 0.04 + 4.50 + 1.51 ≈ **$6.57**. Risk: $45+.

### B. Lambda (arm64, no VPC) + DynamoDB on-demand
- Writes: ~500 WRU/day × 30 = 15,000 × 0.625/1M = **$0.01**
- Reads: 37,500 × ~10 RRU = 375,000 × 0.125/1M = **$0.05**
- Storage: 0.2 GB, inside the 25 GB free tier = **$0.00**. PITR: 0.2 × 0.20 = **$0.04**
- Lambda: **$0.00**
- **Idle** ≈ 0.04 + 1.51 = **$1.55**. **Low traffic** ≈ 0.10 + 1.51 = **$1.61**.

### C. Lambda (arm64, no VPC) + **Aurora DSQL** (single-Region)
- DPU (estimate): AWS's benchmark is ~700,000 TPC-C transactions per 100k DPU, about 0.14 DPU per transaction. I assume a conservative **0.5 DPU per request**; measure it with CloudWatch `TotalDPU` (namespace `AWS/AuroraDSQL`) in dev: 37,500 × 0.5 = 18,750 DPU, inside the 100k free tier = **$0.00**. Requests would have to roughly reach 200k/month before this costs anything, and then $8 per 1M DPU.
- Storage: 0.2 GB, inside the 1 GB free tier = **$0.00**
- AWS Backup daily, 14-day retention (assuming full copies): 14 × 0.2 GB = 2.8 GB-mo × 0.10 = **$0.28**
- No secret is needed, because auth uses IAM tokens. No VPC, no NAT, no IPv4.
- Lambda: **$0.00**
- **Idle** ≈ 0.28 + 1.51 = **$1.79**. **Low traffic** ≈ **$1.79**. At 10× traffic (375k requests): 187,500 DPU − 100k free = 87,500 × $0.000008 = $0.70 more, so ≈ $2.49.

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
| A. Lambda + Aurora SLv2 (0 ACU) + Data API | ~$2.23 | ~$6.57 (up to ~$45 if it never pauses) | Data API secret; anything that keeps it awake; RDS Proxy disables pause | Postgres port + Lambda statelessness; 15–30 s first-request resume |
| B. Lambda + DynamoDB | ~$1.55 | ~$1.61 | none | **Full data-layer rewrite** + Lambda statelessness; SQLite export becomes a translator |
| **C. Lambda + Aurora DSQL** | **~$1.79** | **~$1.79** | backups only | Postgres port with DSQL limits (OCC retries, 3,000-row transactions, no triggers or PL/pgSQL) + Lambda statelessness |
| D. Lightsail + SQLite + Litestream | ~$8.52 | ~$8.62 | instance always on | Smallest: multi-tenant work only; keeps SSE and timers |
| E. Fargate + ALB + RDS t4g.micro | ~$50.14 | ~$50.14 | ALB + IPv4 + always-on DB | Postgres port only |

Lambda statelessness rework (A, B, C) means replacing the in-memory state: the SSE broadcaster `/api/events` becomes polling; the push-attention timers become EventBridge Scheduler one-time schedules; the scanner-mode idle timer becomes a stored expiry; the background product refresh becomes a scheduled Lambda; the telemetry registry and login rate limiter become DB rows. Rough estimate (opinion): about 1–2 weeks of one engineer. See [multi-tenant-saas.md](multi-tenant-saas.md).
