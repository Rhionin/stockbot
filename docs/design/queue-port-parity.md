# Scan queue port: web vs Expo parity (ADR-003 port step 1)

- Status: Draft plan
- Date: 2026-11-24
- Context: [ADR-003](adr-003-one-expo-app-for-web-and-android.md) makes the Expo app (`mobile/`) the only UI. Step 1 makes the Expo queue the web's queue and deletes `frontend/src/components/queue/ScanQueuePage.tsx` and `ScanEntryCard.tsx`.

## Gap

The web queue is about 4.4k lines across 13 components. The Expo `QueueScreen.tsx` is about 1.1k lines. It does pending scans, confirm one or all, backfill, link suggestions and nothing-on-hand. Everything else is web-only today:

| Web feature | Where (frontend/src/components/queue) | Expo today |
|---|---|---|
| Live updates over SSE (`/api/events`): scans, mode, inventory | `ScanQueuePage` (EventSource, `mergeScanEvent`, `scannerModeEvent`) | Polls on focus only (#659) |
| Stock in / Stock out tabs with review counts, mode switch, direction hint | `ScanQueuePage` | Mode switch is on Scan only |
| Scanner connected / disconnected status | `ScanQueuePage` (`getScannerConfig`) | None |
| Unit count and expiry date on a scan | `ScanEntryCard`, `queueUtils` | None |
| Choose which instance to stock out | `StockOutInstanceSelector` | None (#242) |
| Undo after confirm | `ScanEntryCard` (`showUndo`, `undoMove`) | None |
| Batches (sessions), select and select all, batch review panel | `ScanSessionCard`, `BatchReviewPanel`, `queueUtils` | Confirm-all only |
| Name an unknown or flagged product (create, contribute, net size, overrides) | `FlaggedEntryResolver` | None (#244) |
| Group from scan | `ScanEntryCard` (`noteGroupFromScan`, `addGroupMembers`) | None |
| Find an item without a barcode, add item | `FindItemSheet`, `inventory/AddItemSheet` | None (#650) |
| QR misreads and processing cards | `QrScanCard`, `qrPreview`, `ProcessingScanCard` | None (#315) |
| Keyboard-wedge scanner input on the page | `scanner/BarcodeInputField` | None (task: scan on web) |
| Browser scan alerts and web push | `ScanAlertHost`, `browserScanAlert`, `pushAlerts` | None (web-only; native push is #241) |
| Camera on the queue page | `scanner/CameraScanner` | Separate Scan tab |

## Rule for the port: logic moves to `shared/` first

The pure modules (`queueUtils.ts`, `scanAttention.ts`, `qrPreview.ts`, `scannerModeEvent.ts`, `scanLinkInventoryFilter.ts`, `offlineNotice.ts`) move to `shared/`, with their Vitest tests. Both apps import them while the port runs, so the logic is never duplicated. That's the same pattern as #563 and #566. Only the view layer is rebuilt in `mobile/` and `ui/`.

## PR sequence

Each PR keeps `frontend/` working, adds jest-expo real-render tests, and extends `mobile/e2e` with layout-bound and axe checks at 390px and 1280px.

1. **Q1 Shared queue logic.** Move the pure modules above into `shared/`. `frontend` imports them from there. No behavior change.
2. **Q2 Live updates (#659).** One `liveEvents` client: `EventSource` on web, an XHR-based SSE reader on native (works in Expo Go), and polling on error with a stop condition (see #630's bug). Queue, mode and counts follow other devices.
3. **Q3 Queue header.** Stock in / Stock out tabs with counts, the mode switch, the direction hint and scanner status. The landing page on web.
4. **Q4 Card details.** Unit count, expiry, the instance selector (#242), undo after confirm, and partial-removal copy.
5. **Q5 Batches.** Session grouping, selection, select all, and the review panel with batch failure notices.
6. **Q6 Flagged and unknown products (#244).** The resolver with create, contribute, net size and overrides. Product fields become `ui/` components that inventory reuses later.
7. **Q7 Find item and add item (#650).** The sheet works as a modal on native and as a drawer or dialog on web.
8. **Q8 QR and processing cards (#315).**
9. **Q9 Web scanner input.** The keyboard wedge (`ScanScreen.web.tsx` / queue toolbar), self-hosted ZXing WASM, and browser scan alerts.
10. **Q10 Swap.** After the serving ticket in ADR-003, `/` and the queue route serve the Expo build. Move the remaining Playwright specs that cover the queue to `mobile/e2e`. Delete `ScanQueuePage`, `ScanEntryCard` and the queue components nothing else uses.

Q2 to Q8 also fix the phone issues #645 (done), #648, #650, #659, #242 and #244 along the way. The app is the same on both platforms.

Size: about 10 PRs, 3–5 weeks. That's the largest single step of the port, because the queue is the app's most-used screen.
