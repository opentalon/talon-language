// Personalized PageRank (PPR) over the FactStore graph.
//
// `find related` projects the active fact set into an entity-to-entity
// graph (two entities share an edge when they share a (attribute, value)
// pair) and ranks the top-K entities most associated with one or more
// seed entities — HippoRAG-style associative memory.
//
// See docs/design/0002-ppr-fact-graph.md for the algorithm and graph
// model. Determinism, caching, and observability behaviour are described
// there.

find related "Co-consumed stock items" {
  for records where type == "stock_item"
  seeds [801]
  top_k 3
  damping 0.85
  label "{item.name}: ppr score {score}"
  priority MEDIUM
}
