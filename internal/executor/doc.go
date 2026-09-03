// Package executor runs the planned steps of a recipe against the filesystem
// and external commands. It applies idempotency (create/overwrite) and path
// safety, and returns a Result per step. It does NOT abort on error: the caller
// decides abort policy (spec section 30 — partial projects stay for oro apply).
package executor
