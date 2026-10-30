// Package planner converts a resolved recipe into an execution plan: it filters
// steps whose when-condition is false, categorises them (directories/files/
// commands/messages/onboarding) and tags network operations. Onboarding steps
// get a read-only sealed-plan preflight embedded in the plan; the executor
// applies it exclusively through that seal. The plan is consumed by the
// executor and rendered by --dry-run.
package planner
