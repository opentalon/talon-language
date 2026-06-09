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
;; Body: {"query": "[:find ?e :where ...]"}
;; Response: {"results": [[1, 45000], [2, 12000]]}
(defn handle-query [{:keys [body]}]
  (let [query-str (get body "query")
        conn      (:conn @state)
        db        (d/db conn)
        query     (read-string query-str)
        results   (vec (map vec (d/q query db)))]
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

;; GET /health
(defn handle-health [_]
  (resp/response {"status" "ok"}))

(defn routes [{:keys [request-method uri] :as req}]
  (case [request-method uri]
    [:get "/health"]     (handle-health req)
    [:post "/q"]         (handle-query req)
    [:post "/transact"]  (handle-transact req)
    [:post "/schema"]    (handle-schema req)
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
