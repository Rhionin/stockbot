# Update a scan

`PATCH /api/scans/{id}`

Changes a scan that is still open. The body may include any of `direction`, `unitCount`, `expiresAt`, `productId`, and `status`. Omitted fields stay as they are.

`status`, when sent, must be `pending`, `flagged`, or `cancelled`. Sending `committed`, or any other value, is `400` with `"error": "invalid status value"`. A scan becomes committed only through `POST /api/scans/{id}/commit`.

## Open scans

A scan is open when its status is `pending`, `flagged`, or `qr`. Cancelling one of those sets `status` to `cancelled` and returns the updated scan. That is the same request the queue uses for Remove on a pending card, a flagged card, and a QR misread:

```json
{ "status": "cancelled" }
```

`200` returns the scan with `"status": "cancelled"`.

## A scan that was already handled

Every change is one conditional update: the row is written only when its current status is still open (`pending`, `flagged`, or `qr`). That includes a body that changes `direction`, `unitCount`, `expiresAt`, or `productId` and does not mention `status`. A commit that wins the race is not overwritten, and neither is the stock move that commit recorded.

Any change, including status, to a committed or cancelled scan returns `409` `not_open`:

```json
{
  "error": "This scan was already handled.",
  "code": "not_open"
}
```

Nothing is written. A committed scan stays on `GET /api/scans/history` with its stock move unchanged. A cancelled scan stays cancelled. Resolving a flagged scan (`productId`, with or without `status` `pending`) that loses to a remove is the same `409`, and no barcode override is written.

An empty body `{}` on a closed scan returns `200` with no change.

A scan id that does not exist is `404` with `"error": "scan entry not found"`.

Undoing a committed scan is `DELETE /api/moves/{id}` on that stock move. This PATCH does not undo a move.
