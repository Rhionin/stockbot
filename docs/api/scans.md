# Update a scan

`PATCH /api/scans/{id}`

Changes a scan that is still open. The body may include any of `direction`, `unitCount`, `expiresAt`, `productId`, and `status`. Omitted fields stay as they are.

`status`, when sent, must be `pending`, `flagged`, `committed`, or `cancelled`. Any other value is `400` with `"error": "invalid status value"`.

## Open scans

A scan is open when its status is `pending`, `flagged`, or `qr`. Cancelling one of those sets `status` to `cancelled` and returns the updated scan. That is the same request the queue uses for Remove on a pending card, a flagged card, and a QR misread:

```json
{ "status": "cancelled" }
```

`200` returns the scan with `"status": "cancelled"`.

## A scan that was already handled

A status change is one conditional update: the row is written only when its current status is still open. A commit that wins the race is not overwritten, and neither is the stock move that commit recorded.

Sending any status (`cancelled`, `pending`, `flagged`, or `committed`) for a scan that is already `committed` is `409`:

```json
{
  "error": "This scan was already handled.",
  "code": "not_open"
}
```

Nothing is written. The scan stays `committed`, `GET /api/scans/history` still lists it, and the stock move from the commit is unchanged. Other fields sent in the same body, such as `unitCount`, are not applied either.

`not_open` is the same code batch remove uses for a scan that is not open. Issue #314 does not set a sentence for it; this endpoint uses "This scan was already handled."

Undoing a committed scan is `DELETE /api/moves/{id}` on that stock move. This PATCH does not undo a move.
