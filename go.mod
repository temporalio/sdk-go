module go.temporal.io/sdk

go 1.20

replace go.temporal.io/api => ../my-api-go

require (
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a
	github.com/gogo/protobuf v1.3.2
	github.com/gogo/status v1.1.1
	github.com/golang/mock v1.6.0
	github.com/golang/protobuf v1.5.3
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/pborman/uuid v1.2.1
	github.com/robfig/cron v1.2.0
	github.com/stretchr/testify v1.8.4
	go.temporal.io/api v1.24.0
	go.uber.org/atomic v1.9.0
	golang.org/x/sys v0.11.0
	golang.org/x/time v0.3.0
	google.golang.org/grpc v1.57.0
	google.golang.org/protobuf v1.31.0
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/gogo/googleapis v1.4.1 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway v1.16.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/objx v0.5.0 // indirect
	golang.org/x/net v0.14.0 // indirect
	golang.org/x/text v0.12.0 // indirect
	google.golang.org/genproto v0.0.0-20230815205213-6bfd019c3878 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20230815205213-6bfd019c3878 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20230815205213-6bfd019c3878 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
