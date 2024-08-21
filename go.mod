module go.temporal.io/sdk

go 1.21

toolchain go1.21.1

require (
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a
	github.com/gogo/protobuf v1.3.2
	github.com/golang/mock v1.6.0
	github.com/grpc-ecosystem/go-grpc-middleware v1.4.0
	github.com/nexus-rpc/sdk-go v0.0.9
	github.com/pborman/uuid v1.2.1
	github.com/robfig/cron v1.2.0
	github.com/stretchr/testify v1.9.0
	go.temporal.io/api v1.37.1-0.20240821170757-e7d24228ca2b
	golang.org/x/sync v0.8.0
	golang.org/x/sys v0.24.0
	golang.org/x/time v0.3.0
	google.golang.org/grpc v1.65.0
	google.golang.org/protobuf v1.34.2
)

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/google/uuid v1.6.0
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.21.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	golang.org/x/exp v0.0.0-20231127185646-65229373498e
	golang.org/x/net v0.28.0 // indirect
	golang.org/x/text v0.17.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20240814211410-ddb44dafa142 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240814211410-ddb44dafa142 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/nexus-rpc/sdk-go => github.com/rodrigozhou/nexus-rpc-sdk-go v0.0.0-20240821183033-e5054d0a14d7
