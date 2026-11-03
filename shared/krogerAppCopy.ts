import { krogerLabels } from './krogerLabels';

export type StoreNameContext = { store: string; storePossessive: string };

export function storeNameContext(linkedStoreName: string | null | undefined): StoreNameContext {
  if (linkedStoreName !== null && linkedStoreName !== undefined && linkedStoreName.trim() !== '') {
    const store = linkedStoreName.trim();
    return { store, storePossessive: `${store}'s` };
  }
  return { store: 'your store', storePossessive: "your store's" };
}

function withStore(template: string, ctx: StoreNameContext): string {
  return template
    .replaceAll('{store}', ctx.store)
    .replaceAll("{store}'s", ctx.storePossessive)
    .replaceAll('{storePossessive}', ctx.storePossessive);
}

function withKrogerLabel(template: string, label: string): string {
  return template.replaceAll('{k.f.redirect}', label);
}

export const krogerAppCopy = {
  rowTitle: 'Store app',
  rowNotSetUp: 'Not set up yet',
  rowWhyEmpty: "Send your list to your store's online cart.",
  rowSetUp: 'Set up',
  rowReady: 'Ready to connect',
  rowReadyBody:
    'Your store app is saved. Connect your {store} account to send your list.',
  rowReadyBodyFallback:
    'Your store app is saved. Connect your store account to send your list.',
  rowConnect: 'Connect',
  rowConnected: 'Connected',
  rowConnectedBody: 'Your list can go straight to your {store} cart.',
  rowConnectedBodyFallback: "Your list can go straight to your store's cart.",
  rowNoChecks:
    'Checking what {store} has is off. It needs a store app with {k.box.products} ticked.',
  rowNoChecksFallback:
    'Checking what your store has is off. It needs a store app with {k.box.products} ticked.',
  noteUnknown:
    "Your store app works for sending. We haven't checked yet if it can look up what {store} has. Test again to check.",
  noteUnknownFallback:
    "Your store app works for sending. We haven't checked yet if it can look up what your store has. Test again to check.",
  rowNeeds: 'Needs reconnect',
  rowNeedsBody: 'Your {store} account needs to connect again.',
  rowNeedsBodyFallback: 'Your store account needs to connect again.',
  rowReconnect: 'Reconnect {store}',
  rowReconnectFallback: 'Reconnect your store',
  rowLoadFailed: "Couldn't load your store app. Try again.",
  tryAgain: 'Try again',
  menuMore: 'More for store app',
  menuReplace: 'Replace keys',
  menuTest: 'Test again',
  menuRemove: 'Remove store app…',
  testBusy: 'Testing…',
  testOk: 'Your store app works.',
  askAdmin: 'Ask a household admin to set up the store app.',
  askAdminConnect: 'Ask a household admin to connect your {store} account.',
  askAdminConnectFallback: 'Ask a household admin to connect your store account.',
  title: 'Set up your store app',
  progress: (n: number) => `Step ${n} of 7`,
  stepsDisclosure: 'See all steps',
  stepDone: 'Done',
  closeLabel: 'Close setup',
  closeConfirmTitle: 'Close setup?',
  closeConfirmBody:
    "The keys you typed won't be saved. Your place in the steps is saved on this device.",
  closeConfirmKeep: 'Keep going',
  closeConfirmClose: 'Close',
  back: 'Back',
  next: 'Next',
  goStep: (n: number) => `Go to step ${n}`,
  resume: (n: number) => `Welcome back. You stopped at step ${n}.`,
  s1Title: "Sign in to Kroger's developer site",
  s1Body:
    'Your store app is free. You make it on Kroger\'s developer site, and it lets us send your list to your {store} cart.',
  s1BodyFallback:
    "Your store app is free. You make it on Kroger's developer site, and it lets us send your list to your store's cart.",
  s1Open: "Open Kroger's developer site ↗",
  s1OpenA11y: "Open Kroger's developer site (opens in a new tab)",
  s1After: "Come back to this tab when you're signed in.",
  s2Title: 'Start a new app',
  s2Warn: `Choose ${krogerLabels.optProduction}. It can't be changed later.`,
  s3Title: "Fill in Kroger's form",
  s3Body: "Copy each value here, then paste it into the box with the same name on Kroger's page.",
  s3DescValue: "Sends our household's shopping list to our store cart.",
  s3Email: 'Type your own email. Kroger uses it to reach you about your app.',
  s3Exact: "Paste it exactly. If one character is different, connecting won't work.",
  s3NameTaken: 'If Kroger says the name is taken, add a word or number to it.',
  copy: 'Copy',
  copyCopied: 'Copied',
  copyDone: (what: string) => `${what} copied.`,
  copyManual: "Couldn't copy. Press and hold the text to copy it.",
  copyManualDesktop: "Couldn't copy. Select the text and copy it.",
  copyName: 'Copy name',
  copyDesc: 'Copy description',
  copyAddress: 'Copy web address',
  rowLabelName: 'App name',
  rowLabelDesc: 'Description',
  rowLabelEmail: 'Support email',
  rowLabelAddress: 'Web address',
  krogerCallsRedirect: (label: string) => `Kroger calls this box "${label}".`,
  s4Title: 'Tick the boxes',
  s4Warn: "Tick both. If you miss one, you'll have to start a new app.",
  s4IfSeen: 'if you see it',
  s5Title: 'Save your keys',
  s5Once:
    'Kroger shows the secret only once. Keep that page open and go straight to the next step.',
  s6Title: 'Paste your keys',
  s6Body: "Paste both keys from Kroger's page. We'll check them with Kroger before saving.",
  s6Show: 'Show secret',
  s6Hide: 'Hide secret',
  s6Next: "We'll check them with Kroger, then save them.",
  s6Test: 'Test and save',
  s6Testing: 'Testing…',
  modalityLabel: 'How you get your order',
  modalityOptionPickup: 'Pickup',
  modalityOptionDelivery: 'Delivery',
  modalityHelp:
    'Items are added to your {store} cart for this. Change it anytime here.',
  modalityHelpFallback:
    'Items are added to your store cart for this. Change it anytime here.',
  s6Ok: 'Your keys work.',
  s6SavedId: (last4: string) => `Saved app ends in ${last4}.`,
  s6SavedSecret: 'Saved. Paste a new one to replace it.',
  vIdEmpty: 'Paste your App ID.',
  vSecretEmpty: 'Paste your App secret.',
  replaceNote:
    'Your current keys stay in use until the new ones pass Test and save. If they\'re from a different app, everyone in your household will need to connect again.',
  replaceCancel: 'Cancel',
  s7Title: 'Connect your store account',
  s7Body:
    "Sign in with your {store} grocery account and approve. Then you'll come back here.",
  s7BodyFallback:
    "Sign in with your store's grocery account and approve. Then you'll come back here.",
  s7Connect: 'Connect your {store} account ↗',
  s7ConnectFallback: 'Connect your store account ↗',
  s7ConnectAgain: 'Connect your {store} account again ↗',
  s7ConnectAgainFallback: 'Connect your store account again ↗',
  s7ConnectA11y: (store: string) => `Connect your ${store} account (goes to Kroger)`,
  s7Hint:
    "If Kroger says the web address doesn't match, go back to step 3 and copy it again.",
  s7Cancelled: "You didn't finish connecting. Try again when you're ready.",
  errWrongKeys:
    "Those keys didn't work. Check you copied both keys from Kroger's page.",
  noteNoProducts:
    "Saved. You can send your list, but we can't check what {store} has. Checking needs a new app with {k.box.products} ticked.",
  noteNoProductsFallback:
    "Saved. You can send your list, but we can't check what your store has. Checking needs a new app with {k.box.products} ticked.",
  noteNoProductsFix: 'How to start a new app',
  errUnreachable: "Couldn't reach Kroger. Nothing was saved. Try again in a few minutes.",
  errMismatch:
    "Connecting didn't work because the web address in your store app doesn't match this one. Copy it again exactly into the {k.f.redirect} box on Kroger's page, save there, then connect again.",
  errMismatchMember:
    "Connecting didn't work because the store app's web address doesn't match. Ask a household admin to check the store app.",
  errMismatchSaved:
    "Your keys work, but the web address in your store app doesn't match this one. Copy it again exactly into the {k.f.redirect} box on Kroger's page, save there, then test again.",
  errMismatchSavedReplaceSuffix: ' Your current keys are still in use.',
  errMismatchShop:
    "Connecting didn't work because the web address in your store app doesn't match. Open store app setup to copy it again.",
  openSetup: 'Open store app setup',
  errConnectFailed:
    "Couldn't connect your {store} account. Nothing changed. Try again.",
  errConnectFailedFallback: "Couldn't connect your store account. Nothing changed. Try again.",
  errTooMany: 'Too many tests in the last hour. Try again later.',
  vInvalid:
    "Those don't look like the keys. Paste both again from Kroger's page, with no spaces inside.",
  errMismatchCopy: 'Copy the address again',
  errSaveFailed: "Couldn't save your keys. Nothing was saved. Try again.",
  errRemoved: 'Your store app was removed. Start again to set it up.',
  startAgain: 'Start again',
  errNoAddress:
    "This server's web address isn't set up yet, so setup can't go on. Ask your server admin to set it up.",
  linkNeed: 'First, set up your store app. It takes about 10 minutes.',
  linkNotNow: 'Not now',
  linkClose: 'Close',
  rmTitle: 'Remove your store app?',
  rmBody:
    'Your {store} account will disconnect for everyone in your household, and we\'ll stop checking what {store} has. Your stores, lists and trips stay. To send to {store} again, you\'ll need to set up a store app again.',
  rmBodyFallback:
    "Your store account will disconnect for everyone in your household, and we'll stop checking what your store has. Your stores, lists and trips stay. To send to your store again, you'll need to set up a store app again.",
  rmCancel: 'Cancel',
  rmConfirm: 'Remove store app',
  rmBusy: 'Removing…',
  rmDone: 'Store app removed.',
  rmFailed: "Couldn't remove the store app. Try again.",
  checkAppTest:
    'Test your store app so we can check what {store} has.',
  checkAppTestFallback: 'Test your store app so we can check what your store has.',
  checkAppNoProducts: 'Checking needs a store app with {k.box.products} ticked.',
  goStep5: 'Go to step 5',
  goStep3: 'Go to step 3',
  testAgainPrimary: 'Test again',
} as const;

export function krogerAppRowReadyBody(ctx: StoreNameContext): string {
  return ctx.store === 'your store'
    ? krogerAppCopy.rowReadyBodyFallback
    : withStore(krogerAppCopy.rowReadyBody, ctx);
}

export function krogerAppRowConnectedBody(ctx: StoreNameContext): string {
  return ctx.store === 'your store'
    ? krogerAppCopy.rowConnectedBodyFallback
    : withStore(krogerAppCopy.rowConnectedBody, ctx);
}

export function krogerAppNoteUnknown(ctx: StoreNameContext): string {
  return ctx.store === 'your store'
    ? krogerAppCopy.noteUnknownFallback
    : withStore(krogerAppCopy.noteUnknown, ctx);
}

export function krogerAppRowNoChecks(ctx: StoreNameContext): string {
  const template = ctx.store === 'your store'
    ? krogerAppCopy.rowNoChecksFallback
    : krogerAppCopy.rowNoChecks;
  return template.replaceAll('{k.box.products}', `"${krogerLabels.boxProducts}"`);
}

export function krogerAppErrMismatch(kRedirect = krogerLabels.fRedirect): string {
  return withKrogerLabel(krogerAppCopy.errMismatch, kRedirect);
}

export function krogerAppErrMismatchSaved(
  replaceMode: boolean,
  kRedirect = krogerLabels.fRedirect,
): string {
  let text = withKrogerLabel(krogerAppCopy.errMismatchSaved, kRedirect);
  if (replaceMode) text += krogerAppCopy.errMismatchSavedReplaceSuffix;
  return text;
}

export function krogerAppModalityHelp(ctx: StoreNameContext): string {
  return ctx.store === 'your store'
    ? krogerAppCopy.modalityHelpFallback
    : withStore(krogerAppCopy.modalityHelp, ctx);
}

export function krogerAppConnectLabel(ctx: StoreNameContext, again = false): string {
  if (again) {
    return ctx.store === 'your store'
      ? krogerAppCopy.s7ConnectAgainFallback
      : withStore(krogerAppCopy.s7ConnectAgain, ctx);
  }
  return ctx.store === 'your store'
    ? krogerAppCopy.s7ConnectFallback
    : withStore(krogerAppCopy.s7Connect, ctx);
}

/** UX spec G7 + T50 banned jargon for c.app.* source strings. */
export const KROGER_APP_COPY_BANNED =
  /\b(stockbot|oauth|client|scope|credentials|redirect uri|developer app|operator|api|provider|sync|sku|token|lookup|auto|upc)\b/i;

export function collectKrogerAppCopyStrings(): string[] {
  const values: string[] = [];
  for (const entry of Object.values(krogerAppCopy)) {
    if (typeof entry === 'string') values.push(entry);
  }
  return values;
}
