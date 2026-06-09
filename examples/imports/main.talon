// Imports shared.talon's defines and uses them in a detect block.
// Compile + test with:
//   ./talon build examples/imports/main.talon
//   ./talon test  examples/imports/main.talon test/imports.talon.test

import "./shared.talon"

detect "Service overdue" {
  for records where is "active_item" and is "due_for_service"
  flag matching items
  label "{item.name}: service overdue"
  priority HIGH
}
