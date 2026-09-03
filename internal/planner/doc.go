// Package planner converts a resolved recipe into an execution plan: it filters
// steps whose when-condition is false, categorises them (directories/files/
// commands/messages) and tags network operations. The plan is consumed by the
// executor and rendered by --dry-run.
package planner
