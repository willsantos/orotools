// Package conditions evaluates recipe.Condition trees against a resolved
// context (Vars + project base). Used by the planner and executor to decide
// when a step runs (when) or is skipped (skip_if).
package conditions
