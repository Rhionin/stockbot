# API errors

## 503 busy

A request that waits for the pantry database and runs out of time is HTTP 503.
The failure is retryable. The response code is `busy` and the message is
`Stockbot is busy right now. Try again in a moment.`

```json
{"error":"Stockbot is busy right now. Try again in a moment.","code":"busy"}
```

That sentence is the only busy sentence. A full disk is a different 503, code
`no_space`, with its own message. Neither of those messages is replaced by the
generic server sentence (`Something went wrong. Please try again.`), which is
what every other 5xx returns.
