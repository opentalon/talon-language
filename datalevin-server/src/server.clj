(ns server
  (:require [datalevin.core :as d]
            [ring.adapter.jetty :as jetty]
            [ring.middleware.json :refer [wrap-json-body wrap-json-response]]
            [ring.util.response :as resp]
            [clojure.string :as str])
  (:gen-class))

;; State: tenant-id → {:conn ..., :schema ...}. "" is the default
;; tenant served from the root URL (/q, /transact, ...). Named
;; tenants live under /t/<name>/... and get their own DB directory.
(defonce state (atom {}))

(defn- tenant-db-path
  "Per-tenant DB path under DATALEVIN_PATH. The default tenant ('')
   keeps the existing layout so single-tenant callers don't see any
   path change; named tenants get a `<base>/t/<name>` subdir."
  [tenant]
  (let [base (or (System/getenv "DATALEVIN_PATH") "/tmp/talon-datalevin")]
    (if (str/blank? tenant)
      base
      (str base "/t/" tenant))))

(defn init-db!
  "Open (or reopen) a tenant's connection and stash it in state. The
   second arity targets the default tenant; the three-arity form
   takes an explicit tenant key."
  ([db-path schema]
   (init-db! "" db-path schema))
  ([tenant db-path schema]
   (let [conn (d/get-conn db-path schema)]
     (swap! state assoc tenant {:conn conn :schema schema})
     conn)))

(defn- tenant-state
  "Look up (or lazily create) the state map for `tenant`. Lazy
   creation matters because clients can hit /t/<new>/* without an
   explicit bootstrap step — the tenant materializes on first use
   with an empty schema, mirroring how the default tenant opens at
   server start."
  [tenant]
  (or (get @state tenant)
      (do (init-db! tenant (tenant-db-path tenant) {})
          (get @state tenant))))

;; POST /q — execute a Datalog query
;; Body: {"query": "[:find ?e :where ...]"} — minimal form
;;       {"query": "[:find ?e :in $ % :where (r ?e)]", "rules": "[[(r ?e) ...]]"}
;;         — recursive form: query declares `%` in :in, rules carry
;;           the rule definitions as a Datalog vector.
;;       {"query": "...", "as_of": 536870914}
;;         — time-travel: server runs against (d/as-of db tx-id)
;; Response: {"results": [[1, 45000], [2, 12000]]}
(defn handle-query [tenant {:keys [body]}]
  (let [query-str (get body "query")
        rules-str (get body "rules")
        as-of-tx  (get body "as_of")
        conn      (:conn (tenant-state tenant))
        db        (cond-> (d/db conn)
                    (and as-of-tx (pos? as-of-tx)) (d/as-of as-of-tx))
        query     (read-string query-str)
        results   (if (and rules-str (seq rules-str))
                    (vec (map vec (d/q query db (read-string rules-str))))
                    (vec (map vec (d/q query db))))]
    (resp/response {"results" results})))

;; POST /transact — assert facts
;; Body: {"tx-data": [{":record/type": "item", ...}]}
;; Response: {"ok": true, "tx_id": 536870914}
;;
;; tx_id is the basis-t of the transaction Datalevin just committed;
;; clients use it to address that state in later /q calls' as_of.
(defn handle-transact [tenant {:keys [body]}]
  (let [tx-data (get body "tx-data")
        conn    (:conn (tenant-state tenant))
        ;; Convert string keys to keywords
        tx      (mapv (fn [m]
                        (into {} (map (fn [[k v]]
                                        ;; :db/id arrives as a JSON number; preserve
                                        ;; the value verbatim instead of coercing to
                                        ;; keyword like other keys.
                                        [(keyword (subs k 1)) v])
                                      m)))
                      tx-data)
        report  (d/transact! conn tx)]
    (resp/response {"ok" true "tx_id" (get-in report [:tx-data 0 1] 0)})))

;; POST /schema — register schema attributes
;; Body: {"attrs": {":attr/km": {"db/valueType": "db.type/long"}}}
;; Response: {"ok": true}
(defn- coerce-schema-value
  "Schema property values arrive as JSON strings but Datalevin expects
   typed Clojure values: keyword (`:db.type/string`) for type ids, but
   plain booleans for flags like `:db/fulltext`. Map the JSON-string
   form to the right Clojure type so the wire stays string-only."
  [sv]
  (cond
    (vector? sv)         (mapv coerce-schema-value sv)
    (= sv "true")        true
    (= sv "false")       false
    :else                (keyword sv)))

(defn- has-fulltext?
  "True when any attribute in the schema map has :db/fulltext set."
  [schema]
  (some (fn [[_ attr-spec]] (get attr-spec :db/fulltext)) schema))

(defn handle-schema [tenant {:keys [body]}]
  (let [attrs      (get body "attrs")
        new-schema (into {}
                         (map (fn [[k v]]
                                [(keyword (subs k 1))
                                 (into {} (map (fn [[sk sv]]
                                                 [(keyword sk) (coerce-schema-value sv)])
                                               v))])
                              attrs))
        st         (tenant-state tenant)
        prior      (or (:schema st) {})
        merged     (merge-with merge prior new-schema)
        truly-new  (apply dissoc new-schema (keys prior))
        db-path    (tenant-db-path tenant)]
    (when (seq truly-new)
      (if (has-fulltext? truly-new)
        (do
          (d/close (:conn st))
          (init-db! tenant db-path merged))
        (d/update-schema (:conn st) truly-new)))
    (swap! state update tenant assoc :schema merged)
    (resp/response {"ok" true})))

;; POST /retract — retract facts matching the pattern
(defn handle-retract [tenant {:keys [body]}]
  (let [record-id   (get body "record_id")
        attribute   (get body "attribute")
        value       (get body "value")
        conn        (:conn (tenant-state tenant))
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

;; GET /health — no tenant scoping; reports server liveness.
(defn handle-health [_]
  (resp/response {"status" "ok"}))

(defn- parse-uri
  "Split URI into [tenant, suffix]. /t/<name>/<rest> → ['<name>', '/<rest>'];
   anything else → ['', uri]. Default tenant gets the empty string so
   single-tenant callers keep working."
  [uri]
  (if-let [m (re-matches #"^/t/([^/]+)(/.*)$" uri)]
    [(nth m 1) (nth m 2)]
    ["" uri]))

(defn routes [{:keys [request-method uri] :as req}]
  (let [[tenant suffix] (parse-uri uri)]
    (case [request-method suffix]
      [:get  "/health"]    (handle-health req)
      [:post "/q"]         (handle-query tenant req)
      [:post "/transact"]  (handle-transact tenant req)
      [:post "/schema"]    (handle-schema tenant req)
      [:post "/retract"]   (handle-retract tenant req)
      (resp/not-found {"error" "not found"}))))

(def app
  (-> routes
      (wrap-json-body)
      (wrap-json-response)))

(defn -main [& args]
  (let [port    (Integer/parseInt (or (System/getenv "PORT") "8898"))
        db-path (or (System/getenv "DATALEVIN_PATH") "/tmp/talon-datalevin")]
    ;; Bootstrap the default tenant so single-tenant callers see no
    ;; lazy-init latency on first request.
    (init-db! "" db-path {})
    (println (str "datalevin-server listening on port " port))
    (println (str "database path: " db-path))
    (jetty/run-jetty app {:port port :join? true})))
