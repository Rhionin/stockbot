# #231 v2: Restock targets by level (replaces the don't-restock flag)

Status: mockups revised after CJ's review on 2026-10-16. Supersedes draft PR #232. Nothing is implemented. Mockups: `index.html?s=<screen>`, renders in `screenshots/`.

## Decisions
- There is no don't-restock flag. **A target of 0 means don't restock.** The same control sets any target.
- **The most specific level wins: item > group > category > household default.** This holds inside groups too. An item with its own target uses it, even when its group sets one.
- **When an item's categories disagree, the higher target wins.** Any non-zero target beats 0, so "Produce = 0" can't stop garlic that is also in Spices & seasonings.
- **The household default may be 0**, meaning restock nothing unless told to.
- Categories are **shared per household**, seeded from Open Food Facts tags plus suggestions the household confirms.
- **Produce only counts when an item is explicitly fresh.**
- "Don't restock" lives behind the row menu on an item, group, category, or shopping row. Destructive and bulk actions also live there.
- Setting a category or group to 0 first **previews the affected items** and lists the ones that won't change, with the reason.
- **UI copy uses plain source labels**, never programming terms (see "UI copy").

## Behavior (technical)
**Target** = `{ kind: months | amount, value }`. `months` is a supply window (1–12) planned from the usage rate. `amount` is a fixed quantity: a unit count for an item, an ounce-equivalent for a group. `value = 0` means don't restock, whatever the kind.

**Storage**
- Item: `item_targets(household_id, item_id, kind, value)`. This replaces `supply_overrides` (keyed only by `product_id`), `items.target_quantity`, `items.replenishment_mode`, and `product_group_members.no_restock`.
- Group: the existing `product_groups.window_months / quantity_*`, which gains an explicit 0.
- Category: `category_targets(category_id, kind, value)`.
- Household default: today's supply setting, extended to `{kind, value}`. It defaults to 3 months.
- Categories: `categories(id, household_id, name, name_key)` and `item_categories(item_id, category_id, source: off | suggested | user, confirmed_at)`, many-to-many. A household's add or remove is sticky: a later OFF refresh never undoes it.
- Product tags: raw `categories_tags` and `food_groups_tags`, refreshed with the product, with `en:undefined` dropped.

**Resolution**: one function, used by the planner and by every screen.
```
effective(item) =
  item target                                   if set
  else group target                             if the item is in a group that sets one
  else max(targets of the item's categories that set one)
  else household default
-> { target, level: item | group | category | household, levelName, shadowed[] }
```
- **Groups:** a group still plans one shopping line for the members that resolve to the group's target, and its rule picks which member to buy. A member with its own item target leaves the group's pooled line and is planned on its own at that target. 0 means it's never bought, which replaces `no_restock`. It still counts toward the group's on-hand amount for display.
- "Use … target" or "Use household default" deletes the row at the current level. Nothing is copied downward, so later changes at a broader level reach every item that doesn't set its own.
- A manual shopping row still works for an item whose target is 0.

**Category seeding** (see `/workspace/off-categories-findings.md`)
1. A versioned map from about 40 OFF nodes to about 16 starter categories, resolved by walking up `parents`.
2. Fallbacks in this order: `food_groups`, then name keywords and PLU / variable-weight barcode detection, then an LLM later. Anything below step 1 is only a suggestion.
3. Produce needs an `en:fresh-*` node, a PLU code, or the household's check. Anything with a canned, frozen, dried, or sauce ancestor is excluded. `en:fruits` or `en:vegetables` alone is never enough.

## UI copy
Source labels say who set the target, in plain words:

| Level that set it | Label | Fall-back action (row menu and target card) |
|---|---|---|
| Item | "Set on this item" | "Use group target", "Use category target", or "Use household default", naming the next level that has one, with its name and value as the hint ("Snacks & sweets · 2 months") |
| Group | "From Salad kits group" | "Use category target" or "Use household default" |
| Category | "From Snacks & sweets" | "Use household default" |
| Household default | "Household default" | — |

- A category that wins over another one gets a reason in the preview: "From Spices & seasonings, the higher of its two categories."
- 0 shows as "Don't restock" on targets and "Not restocked · <source>" on rows.
- The "won't change" reasons in the preview reuse the same labels: "Set on this item", "Set on this item (in Salad kits group)", "From Salad kits group".
- Banned in copy: inherit / inherited / override / precedence / level names in code form.

## Entry points (mockup screens)
| Where | What | Screen |
|---|---|---|
| Inventory item, expanded | Target card: value, source label, the Item / Group / Category / Default strip with the one in effect highlighted, and Set for this item / Change / Use … target. Category chips plus "+ Category". | `item-from-category`, `item-menu` (1440) |
| Item row menu | Restock target… · Don't restock · Use … target · Categories… · Add to a group… · View instances · History | `item-menu` |
| Collapsed rows | A quiet line only when the target is set on the item or is 0: "Keep 6 · set on this item", "Not restocked · Produce". An Inventory chip "Not restocked (n)" is the review list. | `item-*` |
| Category page | The same target card. Each member row gives its source ("Keep 6, set on this item", "3 months, household default"). Group rows note members with their own target. Menu: Restock target… · Don't restock… · Rename · Merge into… · Delete category… | `category-menu` |
| Category or group → 0 | Preview: "Will stop restocking (n)" with old → 0, and "Won't change (n)" with the reason. The confirm button carries the count. | `category-confirm` (+1440) |
| Shopping row menu | Change target… · Don't restock <item> (immediate, with an Undo notification) · Don't restock <category>… (opens the preview). A group line offers the group plus the member on the line. A manual row offers Remove. | `shop-menu`, `shop-undo` |
| Settings › Restock targets | Household default (Months of supply / Fixed amount, 0 allowed), a "most specific wins" explainer, the categories list with each target and its source, + New category, and a suggestions banner | `settings` |
| Category suggestions | Cards per category with checkboxes and a plain reason per item ("Open Food Facts: Spices", "Loose-produce code"), a Produce card that's fresh-only, and a "Not suggested" line | `suggest` |

## Out of scope for v1
Per-person targets, seasonal or "skip until…" targets, contributing categories back to OFF, and LLM suggestions.

## Open questions for CJ
1. **Which category does a group use** when the group sets no target? The members' shared category, the most common one, or a category assigned to the group explicitly? (Members still resolve on their own when the group has no target.)
2. **Category amounts:** is a category's fixed amount always per item ("Keep 1 each"), or do we also want a shared amount across the category?
3. **Starter categories:** are the 16 names from the findings OK, and can a household delete a starter category entirely?
4. **Undo and history:** how long should Undo stay up (5 seconds?), and should target changes appear in History?
5. **Category page location:** under Inventory (a "Categories" chip, as mocked) or under Settings?
6. **Menu icon:** the app's existing vertical ⋮ (mocked, matches `ItemRow`) or a horizontal ⋯?
7. **Single item, no preview:** "Don't restock" on one item applies immediately with Undo, and only categories and groups get the preview. Is that OK?
