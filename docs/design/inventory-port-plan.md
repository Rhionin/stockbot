# Inventory port plan (ADR-003 port step 2)

- Status: Draft plan
- Date: 2026-11-24
- Depends on: PR #734 merging first. It reshapes the web Inventory into an item detail sheet with list view state (#699, #700, #729). Porting before it lands would port a screen that is about to change.

## What the web Inventory does today

`frontend/src/components/inventory/` is about 2.3k lines, plus `product/ProductEditor` and `SupplyOverride`. It covers:

| Area | Components |
|---|---|
| List with search, filters (`?filter=groups`) and live updates over SSE (`mergeInventoryEvent`) | `InventoryPage`, `ItemRow`, `GroupRow`, `shelf.ts` |
| Product photo with initials fallback | `ProductPhoto`, `photoInitials` |
| Instances: stock in, stock out, expiry, add an instance | `ItemInstanceList`, `AddInstanceModal` |
| Groups: multi-select, add to group, the review inbox (`?review=1`) | `GroupSelectBar`, `AddToGroupSheet`, `groups/InboxPage` |
| Product editor and supply override | `product/ProductEditor`, `SupplyOverride` |
| Restock targets on the item | `restock/TargetCard`, `TargetEditor`, `undo` |
| Opening banner and wipe inventory | `OpeningBanner`, `WipeInventoryDialog` |
| Add item | `AddItemSheet` (already ported in Q7) |

## Rule (same as the queue)

Pure logic moves to `shared/` first: `inventoryUtils`, `shelf.ts`, `photoInitials`, `restock/copy`, `restock/types`, and `utils/inventoryFilter` / `expiry`. Both apps import it from there. Only views are rebuilt in `mobile/` and `ui/`. Q4–Q7 already put the form pieces in `ui/`: `NumberStepper`, `DateField`, `ChoiceList`, `TextField`, `ProductFormFields`, `SheetFrame`, `SearchResults`, `UndoToastView`.

## PR sequence (after #734 merges)

1. **I1 shared logic:** move the modules above. No behavior change.
2. **I2 list:** an Inventory tab (on desktop nav and the phone tab bar) with search, filters, rows, photos, and live updates through the Q2 `LiveEventsProvider`. A virtualized list on native (`FlatList`).
3. **I3 item detail:** the #734 sheet, with instances, stock in/out, expiry and add instance. On native it is a pushed route (`/inventory/[itemId]`). On desktop web it is a side sheet.
4. **I4 product editor and supply override:** reuses `ProductFormFields`.
5. **I5 groups:** multi-select, add to group, and the review inbox.
6. **I6 restock targets** on the item: target card, editor and undo. This overlaps port step 4 (Restock), so share its components.
7. **I7 opening banner and wipe inventory.**
8. **I8 swap:** `/inventory` serves the Expo bundle (needs the serving ticket, `expo-web-serving-ticket.md`). Move the inventory e2e specs and delete the Mantine inventory components.

Size: about 8 PRs, 2–4 weeks.

## Watch
- **Bundle size.** ADR-003's stop-and-ask point is about 500 KB gzip of initial JS after three ports. Measure after I2 with `npx expo export -p web` and record it in the PR.
- **Native lists.** Inventory is the first long list on native. Check scroll performance on a device before I3.
