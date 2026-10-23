// Package clientmsg holds sentences the household sees when a request
// cannot be completed. The real error stays in the server log.
package clientmsg

// ServerError is the sentence for every generic 5xx response.
const ServerError = "Something went wrong. Try again."
