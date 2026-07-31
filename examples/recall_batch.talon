// Recall batch match — string manipulation in conditions and remediate args.
//
// Groundwork for self-hosting (issue #13): string-valued builtin functions
// (upper/lower/trim/length/substring/replace/concat/split/join) usable in any
// expression position. A VIN's first three characters (the world-manufacturer
// prefix) are normalised and matched against a recall batch, and the recall
// ticket's fields are built with the same functions.
//
// Run:
//   go build ./cmd/talon
//   ./talon test examples/recall_batch.talon test/recall_batch.talon.test

detect "Recall batch match" {
  for records where type == "vehicle"
    and upper(substring(attr "vin", 0, 3)) == "1FT"
  flag matching items
  remediate {
    mcp "ops" "open_recall" {
      vehicle attr "id"
      code upper(attr "recall_code")
      region upper(substring(attr "vin", 0, 3))
      title concat("Recall ", attr "vin", " — batch ", upper(attr "recall_code"))
    }
  }
}
