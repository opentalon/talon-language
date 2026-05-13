// Fleet maintenance rules for vehicle service tracking and parts inventory.
//
// Build:
//   go build ./cmd/talon && ./talon build examples/fleet_maintenance.talon
//
// Test queries against Datalevin:
//   brew install datalevin        (if not already installed)
//   rm -rf /tmp/talon-fleet-test
//   dtlv exec -f /dev/stdin < examples/test_datalevin.clj

define "active_vehicle" {
  type == "item"
  and status == "active"
  and category == "Vehicles"
}

define "overdue_km" {
  attr "km" > attr "last_service_km"
}

detect "Service overdue" {
  for records where is "active_vehicle"
    and is "overdue_km"
  flag matching items
  label "{item.name}: {attr.km} km since last service at {attr.last_service_km} km"
  priority HIGH
}

detect "Unusual consumption" {
  for records where type == "stock_item"
    and attr "weekly_consumption" is anomaly compared_to last 12 weeks
  flag matching items
  label "{item.name}: {attr.weekly_consumption} this week (unusual)"
  priority HIGH
}

forecast "Parts stock-out" {
  for records where type == "stock_item" and status == "active"
  series attr "current_stock" over last 90 days
  label "{item.name}: stock-out in ~{days_until} days"
  priority CRITICAL
}

recommend "Schedule service" {
  when detect "Service overdue" matches
  calculate avg_km_weekly from activities within last 90 days
  suggest "Schedule {item.name} for service — averaging {avg_km_weekly} km/week"
  priority HIGH
}

rule "Manager approval for high value" {
  for records where is "active_vehicle"
  before "status_change"
  requires approval from role "manager"
  reason "Fleet vehicles require manager approval for status changes"
}

rule "No assignment during maintenance" {
  for records where type == "item"
    and status == "active"
  block "assign"
  reason "Cannot assign items with open maintenance tickets"
}
