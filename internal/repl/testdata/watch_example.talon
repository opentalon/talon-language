on change attr "current_stock" to 0 {
  logger.warn "stock-out for item {event.entity}"
  workflow "Refill stock"
}

workflow "Refill stock" {
  step "reorder" {
    mcp "inventory" "create-refill-order" {
      item_id  step("trigger").result.entity
      quantity 50
    }
  }
}
