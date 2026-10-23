# Bugfix Requirements Document

## Introduction

When the same barcode is scanned more than once for stock-in (or stock-out) while an earlier scan of that barcode is still sitting unresolved in the scan queue, the system creates a brand new scan entry for every scan instead of recognizing that the item is already represented in the queue. The result is multiple duplicate cards for the same item, each showing unit count 1, rather than a single card whose unit count grows with each additional scan. This inflates the queue with redundant entries and forces the user to manually merge/commit several cards for what should be one line item.

## Bug Analysis

### Current Behavior (Defect)

1.1 WHEN a barcode is scanned and an existing scan entry for that same user, barcode, and direction is already pending in the queue THEN the system creates a new, separate scan entry with unit count 1 instead of updating the existing entry
1.2 WHEN a barcode is scanned and an existing scan entry for that same user, barcode, and direction is already flagged (product not recognized) in the queue THEN the system creates a new, separate scan entry instead of updating the existing entry
1.3 WHEN a barcode is scanned repeatedly in quick succession THEN the system SHALL CONTINUE TO show one duplicate card per scan, so N scans of the same item produce N cards each with unit count 1 instead of one card with unit count N

### Expected Behavior (Correct)

2.1 WHEN a barcode is scanned and an existing pending scan entry for that same user, barcode, and direction is already in the queue THEN the system SHALL increment the unit count on the existing entry instead of creating a new entry. This holds for stock-in and for stock-out. A rescan does not remove anything: nothing leaves the shelf until that stock-out entry is committed. Committing a stock-out whose unit count is N removes N on-hand units in one transaction. When the card names an instance, that unit goes first and the rest come off earliest expiry first. The same expiry goes earliest acquired first, then lowest id. Undated units go last, also earliest acquired first, then lowest id. Otherwise every unit comes off that same order. When fewer than N are on hand and at least one is, the commit returns 200 and removes every unit on hand. That 200 body, and every other successful stock-out commit, includes `removedCount` (units actually removed) and `requestedCount` (N). Clients show the short-shelf note when `removedCount` is less than `requestedCount`. When nothing is on hand, the commit stays 409 `none_on_hand` with "Nothing on hand to take out." and the shelf is unchanged. A named instance that is already gone stays `instance_gone` when another unit remains, and `none_on_hand` when the shelf is empty.
2.2 WHEN a barcode is scanned and an existing flagged scan entry for that same user, barcode, and direction is already in the queue THEN the system SHALL increment the unit count on the existing entry instead of creating a new entry
2.3 WHEN a scan is merged into an existing entry THEN the system SHALL keep the existing entry's original scanned-at timestamp unchanged (it SHALL NOT be replaced by the later scan's timestamp)

### Unchanged Behavior (Regression Prevention)

3.1 WHEN a barcode is scanned and no existing pending or flagged entry for that user, barcode, and direction exists in the queue THEN the system SHALL CONTINUE TO create a new scan entry with unit count 1
3.2 WHEN a barcode is scanned and the only existing entry for that user and barcode has a different direction (e.g. an existing stock-out entry when the new scan is stock-in) THEN the system SHALL CONTINUE TO create a new, separate scan entry rather than merging across directions
3.3 WHEN a barcode is scanned and an existing entry for that same barcode belongs to a different user THEN the system SHALL CONTINUE TO create a new scan entry rather than merging across users
3.4 WHEN a barcode is scanned and the only existing entries for that user, barcode, and direction are already committed or cancelled THEN the system SHALL CONTINUE TO create a new scan entry, since committed/cancelled entries are not eligible for merging
3.5 WHEN a scan entry is created through the web scan-in field or through the headless barcode-scanner listener THEN the system SHALL CONTINUE TO apply the same merge-or-create rule consistently for both entry points
