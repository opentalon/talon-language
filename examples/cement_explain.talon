// Tier-1 explainability demo — see docs/design/0003-explainability.md.
//
// A detect block flags low stock; a recommend block fires when the
// detect matches. `talon explain` renders the user-facing "why" + the
// chain from recommendation back through the triggering detect.
//
//   go build ./cmd/talon
//   ./talon explain examples/cement_explain.talon test/cement_explain.talon.test

detect "Cement running low" {
  for records where type == "stock_item"
    and attr "current_stock" <= attr "minimum_amount"
  flag matching items
  label "{item.name}: {attr.current_stock} bags left (minimum: {attr.minimum_amount})"
  priority CRITICAL
}

recommend "Order cement" {
  when detect "Cement running low" matches
  suggest "Order {item.name} — currently {attr.current_stock} bags, below minimum {attr.minimum_amount}"
  priority HIGH
}
