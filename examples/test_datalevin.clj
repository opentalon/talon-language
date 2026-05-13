(require '[datalevin.core :as d])

(def schema
  {:record/type          {:db/valueType :db.type/string}
   :record/status        {:db/valueType :db.type/string}
   :record/category      {:db/valueType :db.type/string}
   :attr/name            {:db/valueType :db.type/string}
   :attr/km              {:db/valueType :db.type/long}
   :attr/last_service_km {:db/valueType :db.type/long}
   :attr/current_stock   {:db/valueType :db.type/long}})

(def conn (d/get-conn "/tmp/talon-fleet-test" schema))

(d/transact! conn
  [{:record/type "item" :record/status "active" :record/category "Vehicles"
    :attr/name "Truck A" :attr/km 45000 :attr/last_service_km 20000}
   {:record/type "item" :record/status "active" :record/category "Vehicles"
    :attr/name "Van B" :attr/km 12000 :attr/last_service_km 11000}
   {:record/type "item" :record/status "active" :record/category "Vehicles"
    :attr/name "Car C" :attr/km 5000 :attr/last_service_km 5000}
   {:record/type "stock_item" :record/status "active"
    :attr/name "Brake Pads" :attr/current_stock 15}
   {:record/type "stock_item" :record/status "active"
    :attr/name "Oil Filters" :attr/current_stock 200}])

(def db (d/db conn))

(def results
  {:service-overdue
   (d/q '[:find ?e ?km ?last_service_km
          :where
          [?e :record/type "item"]
          [?e :record/status "active"]
          [?e :record/category "Vehicles"]
          [?e :attr/km ?km]
          [?e :attr/last_service_km ?last_service_km]
          [(> ?km ?last_service_km)]]
     db)

   :manager-approval
   (d/q '[:find ?e
          :where
          [?e :record/type "item"]
          [?e :record/status "active"]
          [?e :record/category "Vehicles"]]
     db)

   :no-assign
   (d/q '[:find ?e
          :where
          [?e :record/type "item"]
          [?e :record/status "active"]]
     db)

   :unusual-consumption
   (d/q '[:find ?e
          :where
          [?e :record/type "stock_item"]]
     db)

   :parts-stockout
   (d/q '[:find ?e
          :where
          [?e :record/type "stock_item"]
          [?e :record/status "active"]]
     db)})

(d/close conn)

results
