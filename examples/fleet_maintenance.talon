// Fleet maintenance rules for vehicle service tracking and parts inventory.
//
// Build:
//   go build ./cmd/talon && ./talon build examples/fleet_maintenance.talon
//
// Seeded end-to-end run (requires JDK 21 + Clojure CLI; no `brew install datalevin`):
//   brew install openjdk@21 clojure/tools/clojure
//   rm -rf /tmp/talon-datalevin
//   (cd datalevin-server && clojure -M:run)                 # terminal 1
//   ./talon run examples/fleet_maintenance.talon \          # terminal 2
//     --seed test/fleet_maintenance.talon.test
//
// Ad-hoc REPL against the native dtlv CLI (optional; download release zip — no brew formula):
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
