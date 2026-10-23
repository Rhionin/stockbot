// Package clientmsg holds sentences the household sees when a request
// cannot be completed. The real error stays in the server log.
package clientmsg

// MigrationUpdating is the sentence shown while a deploy is still applying
// database migrations. The web and mobile apps use the same words.
const MigrationUpdating = "Stockbot is updating. This takes about a minute."
