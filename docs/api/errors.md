# API errors

## 503 busy

A request that waits for the pantry database and runs out of time is HTTP 503.
Any route that reads or writes the database can return it. The failure is
retryable. The response code is `busy` and the message is
`Stockbot is busy right now. Try again in a moment.`

```json
{"error":"Stockbot is busy right now. Try again in a moment.","code":"busy"}
```

That sentence is the only busy sentence. The response does not include a
`Retry-After` header.

## 503 no space

When SQLite cannot save a change because the disk is full, the status is also
503. The code is `no_space` and the message is
`Stockbot is out of space to save changes.`

```json
{"error":"Stockbot is out of space to save changes.","code":"no_space"}
```

This response does not include a `Retry-After` header either.

## Other 5xx

Every other 5xx uses the generic sentence
`Something went wrong. Please try again.` The body has no `code`. The server
does not emit `internal`.

The generic sentence does not replace the busy sentence or the no-space sentence.

## 4xx

A 4xx stays a 4xx when the underlying cause is a database lock or a full disk.
The response reports the client error.
