(ns server
  (:require [datalevin.core :as d]
            [ring.adapter.jetty :as jetty]
            [ring.middleware.json :refer [wrap-json-body wrap-json-response]]
            [ring.util.response :as resp])
  (:gen-class))

(defonce state (atom nil))

(defn init-db! [db-path schema]
  (let [conn (d/get-conn db-path schema)]
    (reset! state {:conn conn :schema schema})
    conn))

;; POST /q — execute a Datalog query
;; Body: {"query": "[:find ?e :where ...]"} — minimal form
;;       {"query": "[:find ?e :in $ % :where (r ?e)]", "rules": "[[(r ?e) ...]]"}
;;         — recursive form: query declares `%` in :in, rules carry
;;           the rule definitions as a Datalog vector.
;; Response: {"results": [[1, 45000], [2, 12000]]}
;;
;; When rules is supplied, the query MUST declare `%` in :in so the
;; rule vector lines up positionally with d/q's variadic args. The
;; client renders that automatically when factstore.Query.Rules is
;; non-empty.
(defn handle-query [{:keys [body]}]
  (let [query-str (get body "query")
        rules-str (get body "rules")
        conn      (:conn @state)
        db        (d/db conn)
        query     (read-string query-str)
        results   (if (and rules-str (seq rules-str))
                    (vec (map vec (d/q query db (read-string rules-str))))
                    (vec (map vec (d/q query db))))]
    (resp/response {"results" results})))

;; POST /transact — assert facts
;; Body: {"tx-data": [{":record/type": "item", ...}]}
;; Response: {"ok": true}
(defn handle-transact [{:keys [body]}]
  (let [tx-data (get body "tx-data")
        conn    (:conn @state)
        ;; Convert string keys to keywords
        tx      (mapv (fn [m]
                        (into {} (map (fn [[k v]] [(keyword (subs k 1)) v]) m)))
                      tx-data)]
    (d/transact! conn tx)
    (resp/response {"ok" true})))

;; POST /schema — register schema attributes
;; Body: {"attrs": {":attr/km": {"db/valueType": "db.type/long"}}}
;; Response: {"ok": true}
;;
;; The incoming attrs are MERGED into the existing schema map and
;; applied via datalevin.core/update-schema. The previous implementation
;; closed and reopened the DB with only the new attrs, which silently
;; dropped previously-registered attrs from the active schema (#78).
;; Facts in LMDB survive that pattern, but the runtime forgets the
;; attribute types — any further query against a "lost" attr falls
;; back to coarse coercion. The merge form is the correct shape.
(defn handle-schema [{:keys [body]}]
  (let [attrs      (get body "attrs")
        new-schema (into {}
                         (map (fn [[k v]]
                                [(keyword (subs k 1))
                                 (into {} (map (fn [[sk sv]]
                                                 [(keyword sk) (keyword sv)])
                                               v))])
                              attrs))
        conn       (:conn @state)
        prior      (or (:schema @state) {})
        merged     (merge prior new-schema)]
    (when (seq new-schema)
      (d/update-schema conn new-schema))
    (swap! state assoc :schema merged)
    (resp/response {"ok" true})))

;; POST /retract — retract facts matching the pattern
;; Body shapes:
;;   {"record_id": "501"}                                        — drop whole entity
;;   {"record_id": "501", "attribute": ":attr/km"}               — drop one attribute (any value)
;;   {"record_id": "501", "attribute": ":attr/km", "value": 50}  — drop one specific cell
;; Response: {"ok": true, "retracted": N}
;;
;; record_id is the entity ID as a string of digits. The handler
;; parses it back to an int because Datalevin's tx-data wants the
;; numeric entity ID. When `attribute` is omitted the entire entity
;; is removed via :db.fn/retractEntity. When `attribute` is set but
;; `value` is omitted the handler queries Datalevin for the current
;; value(s) and retracts each — :db/retract requires a value because
;; Datalevin stores history of every (entity, attribute, value)
;; triple.
(defn handle-retract [{:keys [body]}]
  (let [record-id   (get body "record_id")
        attribute   (get body "attribute")
        value       (get body "value")
        conn        (:conn @state)
        eid         (Long/parseLong record-id)]
    (cond
      (nil? attribute)
      (do (d/transact! conn [[:db.fn/retractEntity eid]])
          (resp/response {"ok" true "retracted" 1}))

      (nil? value)
      (let [db      (d/db conn)
            attr-kw (keyword (subs attribute 1))
            vals    (d/q '[:find ?v
                           :in $ ?e ?a
                           :where [?e ?a ?v]]
                         db eid attr-kw)]
        (d/transact! conn
                     (vec (for [[v] vals] [:db/retract eid attr-kw v])))
        (resp/response {"ok" true "retracted" (count vals)}))

      :else
      (let [attr-kw (keyword (subs attribute 1))]
        (d/transact! conn [[:db/retract eid attr-kw value]])
        (resp/response {"ok" true "retracted" 1})))))

;; GET /health
(defn handle-health [_]
  (resp/response {"status" "ok"}))

(defn routes [{:keys [request-method uri] :as req}]
  (case [request-method uri]
    [:get "/health"]     (handle-health req)
    [:post "/q"]         (handle-query req)
    [:post "/transact"]  (handle-transact req)
    [:post "/schema"]    (handle-schema req)
    [:post "/retract"]   (handle-retract req)
    (resp/not-found {"error" "not found"})))

(def app
  (-> routes
      (wrap-json-body)
      (wrap-json-response)))

(defn -main [& args]
  (let [port    (Integer/parseInt (or (System/getenv "PORT") "8898"))
        db-path (or (System/getenv "DATALEVIN_PATH") "/tmp/talon-datalevin")]
    ;; Start with empty schema; /schema endpoint will configure it
    (init-db! db-path {})
    (println (str "datalevin-server listening on port " port))
    (println (str "database path: " db-path))
    (jetty/run-jetty app {:port port :join? true})))
