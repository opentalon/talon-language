package validator

import "testing"

func TestValidateDeriveClean(t *testing.T) {
	mustClean(t, `
derive overdue(v) {
  for records where type == "vehicle"
    and attr "km" > attr "last_service_km" + 20000
}

detect "Recall candidates" {
  for records where overdue(v) and attr "model" in ["Transit", "Sprinter"]
  flag matching items
  priority HIGH
}`)
}

func TestValidateDeriveChain(t *testing.T) {
	// A derive referencing another derive (non-recursive) is fine.
	mustClean(t, `
derive overdue(v) {
  for records where type == "vehicle" and attr "km" > attr "last_service_km" + 20000
}

derive due_for_recall(v) {
  for records where overdue(v) and attr "model" in ["Transit", "Sprinter"]
}

detect "Recall" {
  for records where due_for_recall(v)
  flag matching items
}`)
}

func TestValidateUndeclaredDeriveRef(t *testing.T) {
	mustError(t, `
detect "Recall" {
  for records where missing_pred(v)
  flag matching items
}`, `undeclared derived predicate "missing_pred"`)
}

func TestValidateDeriveDirectCycle(t *testing.T) {
	mustError(t, `
derive a(v) {
  for records where b(v)
}

derive b(v) {
  for records where a(v)
}`, "recursive derive cycle")
}

func TestValidateDeriveSelfCycle(t *testing.T) {
	mustError(t, `
derive loop(v) {
  for records where type == "x" and loop(v)
}`, "recursive derive cycle")
}
