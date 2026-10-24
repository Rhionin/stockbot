-- Tables that already stored household_id used the legacy user id.
-- Point those rows at household 1. A value other than that legacy id
-- is rejected before this file runs.

UPDATE categories
SET household_id = '{{household}}'
WHERE household_id IN ('user-1', '');

UPDATE category_targets
SET household_id = '{{household}}'
WHERE household_id IN ('user-1', '');

UPDATE item_targets
SET household_id = '{{household}}'
WHERE household_id IN ('user-1', '');

UPDATE item_categories
SET household_id = '{{household}}'
WHERE household_id IN ('user-1', '');

UPDATE category_suggestion_dismissals
SET household_id = '{{household}}'
WHERE household_id IN ('user-1', '');
