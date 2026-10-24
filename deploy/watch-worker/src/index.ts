import {
  backupDue,
  r2Store,
  runWatch,
  type EmailMessageCtor,
  type RunReport,
  type WatchBindings,
} from './watch';

export {
  backupDue,
  backupPrefixes,
  buildRawMime,
  buildUrl,
  checkBackups,
  checkUptime,
  deliverEmail,
  evaluateBackups,
  formatDuration,
  formatInstant,
  healthcheckTarget,
  manifestKeyFor,
  parseWatchState,
  r2Store,
  runWatch,
  transition,
} from './watch';

export type {
  AlertKind,
  CheckResult,
  EmailMessageCtor,
  ListedObject,
  ObjectStore,
  R2BucketLike,
  RunReport,
  WatchBindings,
} from './watch';

interface WatchEnv extends Env {
  HC_UPTIME_URL?: string;
  HC_BACKUP_URL?: string;
  BACKUP_DRILL_PREFIX?: string;
}

let emailCtor: Promise<EmailMessageCtor | null> | undefined;

export function loadEmailMessageCtor(): Promise<EmailMessageCtor | null> {
  emailCtor ??= import('cloudflare:email')
    .then((mod) => (typeof mod.EmailMessage === 'function' ? mod.EmailMessage : null))
    .catch(() => null);
  return emailCtor;
}

function bindings(env: WatchEnv): WatchBindings {
  return {
    TARGET_URL: env.TARGET_URL,
    ALERT_TO: env.ALERT_TO,
    ALERT_FROM: env.ALERT_FROM,
    BACKUPS: r2Store(env.BACKUPS),
    // Production passes an EmailMessage. Vitest, which has no cloudflare:email
    // module, passes the same MIME string. The cast keeps the binding's
    // EmailMessage parameter without changing the runtime value.
    send: (message) => env.EMAIL.send(message as EmailMessage),
    HC_UPTIME_URL: env.HC_UPTIME_URL,
    HC_BACKUP_URL: env.HC_BACKUP_URL,
    BACKUP_DRILL_PREFIX: env.BACKUP_DRILL_PREFIX,
  };
}

export async function handleScheduled(scheduledTime: number, env: WatchEnv): Promise<RunReport> {
  return runWatch(bindings(env), {
    nowMs: scheduledTime,
    runBackup: backupDue(scheduledTime),
    emailCtor: await loadEmailMessageCtor(),
  });
}

export async function handleManual(env: WatchEnv, nowMs: number): Promise<RunReport> {
  return runWatch(bindings(env), {
    nowMs,
    runBackup: true,
    emailCtor: await loadEmailMessageCtor(),
  });
}

export default {
  async scheduled(controller, env, ctx): Promise<void> {
    void ctx;
    await handleScheduled(controller.scheduledTime, env);
  },
  async fetch(request, env, ctx): Promise<Response> {
    void ctx;
    const url = new URL(request.url);
    if (url.pathname !== '/run' || (request.method !== 'GET' && request.method !== 'POST')) {
      return new Response('not found', { status: 404 });
    }
    const report = await handleManual(env, Date.now());
    return Response.json(report, { headers: { 'cache-control': 'no-store' } });
  },
} satisfies ExportedHandler<WatchEnv>;
