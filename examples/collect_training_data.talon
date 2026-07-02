// Scheduled MCP-driven fact ingestion. Talon describes WHAT to collect and
// WHEN; the host (OpenTalon, a k8s CronJob, systemd timer, ...) reads the
// schedule via `talon collect list` and fires `talon collect run` on time.
// Talon itself runs no scheduler — see docs and issue #57.
//
//   talon collect list examples/collect_training_data.talon
//   talon collect run  examples/collect_training_data.talon --name "Failure training data"

collect "Failure training data" {
  schedule weekly
  mcp "inventory" "list-items" {
    query    "status:defective"
    per_page 100
    on_error {
      retry 3 times
      then log "collect failed: {error}"
      then skip
    }
  }
  store results as training_facts tag "failure_training"
}

collect "Daily stock snapshot" {
  schedule every 6 hours
  mcp "inventory" "list-stock-items" {}
  store results as stock_snapshot tag "stock"
}
