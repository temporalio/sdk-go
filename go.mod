module go.temporal.io/sdk

go 1.16

require (
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a
	github.com/gogo/protobuf v1.3.2
	github.com/gogo/status v1.1.0
	github.com/golang/mock v1.6.0
	github.com/golang/protobuf v1.5.2
	github.com/google/uuid v1.3.0 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware v1.3.0
	github.com/kr/text v0.2.0 // indirect
	github.com/niemeyer/pretty v0.0.0-20200227124842-a10e7caefd8e // indirect
	github.com/opentracing/opentracing-go v1.2.0
	github.com/pborman/uuid v1.2.1
	github.com/robfig/cron v1.2.0
	github.com/stretchr/objx v0.3.0 // indirect
	github.com/stretchr/testify v1.7.0
	github.com/twmb/murmur3 v1.1.6 // indirect
	github.com/uber-go/tally/v4 v4.0.1
	go.opentelemetry.io/otel v1.1.0
	go.opentelemetry.io/otel/sdk v1.1.0
	go.opentelemetry.io/otel/trace v1.1.0
	go.temporal.io/api v1.5.0
	go.uber.org/atomic v1.9.0
	go.uber.org/goleak v1.1.11
	golang.org/x/time v0.0.0-20210723032227-1f47c861a9ac
	google.golang.org/grpc v1.41.0
	google.golang.org/protobuf v1.27.1
	gopkg.in/check.v1 v1.0.0-20200227125254-8fa46927fb4f // indirect
	gopkg.in/yaml.v3 v3.0.0-20210107192922-496545a6307b // indirect
)

// TODO(cretz): Temporary, remove and update tag in require section before merge
replace go.temporal.io/api => ../temporal-api-go
