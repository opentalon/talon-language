detect "Loaded block" {
  for records where type == "item"
  flag matching items
  label "loaded"
}
