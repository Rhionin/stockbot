# Can Open Food Facts seed Stockbot categories? (research, 2026-10-16)

## TL;DR
OFF is a solid **starting point for shelf-stable pantry categories**, but a **poor detector of Produce**. On CJ's real pantry, 48% of products map automatically to one of 16 starter categories through the taxonomy alone, and about 54% when food_groups is used as a fallback. The rest need a suggestion layer (name heuristics or an LLM) plus household confirmation. Produce mostly has no barcode, and when OFF does tag fruit or veg it usually doesn't say whether it's fresh.

## Data
- Taxonomy: https://static.openfoodfacts.org/data/taxonomies/categories.json has 14,758 nodes. Each node has `parents`, so it forms a DAG, and some nodes have several parents. Source: https://github.com/openfoodfacts/openfoodfacts-server/blob/main/taxonomies/food/categories.txt
- Product API: `https://world.openfoodfacts.org/api/v2/product/<barcode>.json?fields=categories_tags,food_groups_tags,...`. I fetched 283 products at about 1 request per second with a UA string.
- Other fields: `food_groups_tags` (PNNS / Nutri-Score groups, such as en:fruits-and-vegetables > en:fruits, en:beverages, en:cereals-and-potatoes, en:fats-and-sauces, en:sugary-snacks) is coarse and only present on about half of products. `pnns_groups_1/2` is the legacy version of the same thing.

## Relevant taxonomy nodes
| Starter category | OFF nodes (match on node or any descendant) |
|---|---|
| Produce | en:fresh-fruits (29 children), en:fresh-vegetables (34), en:fresh-plant-based-foods. **Not** en:fruits / en:vegetables, which also cover canned, frozen and dried. |
| Breads & bakery | en:breads (41 children; parent en:cereals-and-their-products), en:viennoiseries, en:cakes, en:tortillas |
| Spices & seasonings | en:spices (32), en:herbs-and-spices, en:herbs, en:seasonings, en:salts |
| Canned & jarred | en:canned-foods, en:canned-plant-based-foods, en:canned-soups, en:canned-meals (en:canned-fruits sits under both canned and fruit) |
| Beverages | en:beverages, en:beverages-and-beverages-preparations, en:dehydrated-beverages |
| Snacks & sweets | en:snacks, en:sweet-snacks, en:salty-snacks, en:confectioneries |
| Dairy & eggs | en:dairies (20), en:milks, en:cheeses, en:eggs |
| Others used | Frozen (en:frozen-foods, 68), Pasta/rice/grains, Breakfast cereals, Condiments & sauces, Oils & vinegars, Baking, Meat & seafood, Legumes & nuts, Spreads & sweeteners |

## Coverage on CJ's pantry (283 products, all with barcodes)
- Local DB: 283 products and 285 barcodes. `products.category` is non-null on 258, but 37 of those are the literal string `"undefined"`, which OFF returns as the tag `en:undefined`, and 52 are null. The values are OFF's comma-separated display names, such as "Cereals and their products, Pastas", "Canned soups" and "Tomatoes, Tomato pastes". That makes them flat, English, and not usable as ids. **We should store `categories_tags` instead.**
- OFF lookups: **259 / 283 (92%) found** (21 returned 404, 3 failed).
- **With any category tag: 208 (73%)**. Of those, 42 are `en:undefined` only, so about **166 (59%) have real tags**.
- **Mapped to a starter category through ancestor walk: 137 (48%)**. 105 got one category, and 32 got 2–4, which is normal for many-to-many membership (e.g. Garbanzo Beans goes to Canned + Legumes; Iodized Salt goes to Spices + Condiments, via en:groceries).
- food_groups fallback adds 16 more (about 54%), but it's coarse ("fruits-and-vegetables").
- Distribution: Pasta/grains 40, Condiments 34, Canned 25, Beverages 12, Spreads 10, Breads 10, Dairy 9, Oils 7, Snacks 7, Legumes 6, Baking 6, Spices 4, Cereal 4, Meat 2, **Produce 1**.

Good mappings: Canola Oil goes to Oils (en:canola-oils); Lasagna/Farfalle to Pasta; Diced tomatoes to Canned (en:canned-tomatoes); Beef Broth to Canned (en:canned-soups); Peanut butter to Legumes + Spreads; Parmesan to Dairy.

Misses:
- No tags or en:undefined (most misses): pinto/kidney/cannellini beans, granulated sugar, vanilla extract, condensed soups, soy sauce, olives, mushrooms, preserves, panko.
- Tags that are too loose or non-canonical: "en:canned mandarin oranges", "en:Powdered Milk", "en:Cooking spray". These are free-text tags that aren't in the taxonomy, so they have no parents.
- Form unknown: Grapes is tagged en:fruits but not en:fresh-fruits. Crushed tomatoes and pumpkin puree are tagged en:vegetables with no canned tag.
- Fruit/veg form across 31 items: 14 canned, **1 fresh** (Raspberries), and 16 unspecified. The unspecified ones include Grapes, cherry tomatoes, marinara, frozen OJ concentrate and canned tomatoes. Only the *fresh-* nodes are a safe Produce signal, and they're rare.

## Produce specifically
- Loose produce uses PLU codes (4–5 digits, IFPS) or store-printed variable-weight barcodes (UPC prefix 2 / EAN 20–29). It has no global barcode, so OFF can't help. Packaged produce (berries, salad bags) sometimes has a UPC and is tagged fresh-* inconsistently.
- Recommendation: have Produce membership come mainly from (a) the en:fresh-* nodes, (b) PLU / variable-weight barcode detection, (c) name heuristics or an LLM suggestion for items without a barcode, with the household confirming.
- Risk: matching on en:fruits or en:vegetables would put canned pears, frozen OJ and marinara into Produce. Then "Produce = target 0" would silently stop restocking pantry staples. Require a fresh-* node or explicit confirmation, and exclude anything that has a canned, frozen, dried or sauce ancestor.

## Proposed approach
1. **Store raw `categories_tags` (plus `food_groups_tags`) per product**, refreshed with the product, and drop `en:undefined`.
2. **Curated mapping** (a config file, versioned) from about 40 OFF nodes to 12–20 starter categories. Resolve by walking `parents` to the root, so specific tags like en:spaghetti roll up to Pasta. Add exclusion rules (e.g. Produce requires fresh-* and has no canned/frozen/dried ancestor).
3. **Fallbacks, in order:** food_groups (coarse), then name keywords / PLU detection, then an LLM suggestion for whatever is left. Everything below step 2 is *suggested*, never applied silently.
4. **Household owns the result.** Starter categories get created on first use, and memberships are many-to-many and editable. Show a "These 9 items look like Spices — add?" review, and once the user overrides an assignment, that override sticks over later refreshes.
5. **Safety for target 0:** when someone sets a category to 0, list the affected items before saving. Combined with "restock wins" on conflicts, this limits the damage from bad tags.
6. Optionally, contribute fixed categories back to OFF. The app already has a product_contributions path.

Expected outcome for a typical pantry: about 50–60% auto-seeded, about 30% suggested well, and the rest manual. Produce is mostly manual or heuristic.
