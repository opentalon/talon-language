module github.com/opentalon/talon-language

go 1.25.0

replace github.com/opentalon/talon-db => ../talon-db

require (
	github.com/opentalon/talon-db v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.81.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/RoaringBitmap/roaring/v2 v2.18.2 // indirect
	github.com/bits-and-blooms/bitset v1.24.2 // indirect
	github.com/golang/snappy v1.0.0 // indirect
	github.com/mschoch/smat v0.2.0 // indirect
	go.etcd.io/bbolt v1.5.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
)
