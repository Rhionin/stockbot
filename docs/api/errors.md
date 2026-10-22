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
`Stockbot is out of storage space, so this wasn't saved.`

```json
{"error":"Stockbot is out of storage space, so this wasn't saved.","code":"no_space"}
```

This response does not include a `Retry-After` header.

Each of these responses also writes one warning log line. The message is
`db_no_space`. The attributes are `event=db_no_space`, `method`, `route`,
`sqlite_code` (the driver result code, not the driver text), and
`household_id` when the server knows the household. The line has no SQL and
no names. Operator alerts for #224 match that line. This does not add an alert.

## Other 5xx

Every other 5xx uses the generic sentence
`Something went wrong. Please try again.` The body has no `code`. The server
does not emit `internal`.

The generic sentence does not replace the busy sentence or the no-space sentence.

## 4xx

A 4xx stays a 4xx when that 4xx is already decided. The response reports the
client error, including when the cause is a database lock or a full disk.

Some conflicts are decided only after the write transaction starts. Under
write contention those requests can wait up to 3 seconds and then come back
as a busy 503. That covers renaming a group to a name that already exists,
and adding a product that is already in another group.

Creating a group checks the name before the write transaction. A duplicate
name is a 409 while another writer holds a reserved lock. An exclusive lock
blocks that read as well, so the same duplicate check can wait and return a
busy 503.
