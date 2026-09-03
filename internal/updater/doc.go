// Package updater checks and applies oro releases from the public GitHub
// repository (willsantos/orotools). It wraps go-selfupdate in GitHub mode with
// sha256 validation against the release checksums.txt, and keeps the update
// channel swappable: a custom BaseURL is all it takes to point elsewhere.
package updater
