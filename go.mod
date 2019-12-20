module go.temporal.io/temporal

go 1.12

require (
	github.com/apache/thrift v0.0.0-20161221203622-b2a4d4ae21c7
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a
	github.com/gogo/googleapis v1.3.1 // indirect
	github.com/gogo/protobuf v1.3.1 // indirect
	github.com/gogo/status v1.1.0 // indirect
	github.com/golang/mock v1.3.1
	github.com/opentracing/opentracing-go v1.1.0
	github.com/pborman/uuid v0.0.0-20160209185913-a97ce2ca70fa
	github.com/prometheus/client_golang v1.2.1 // indirect
	github.com/robfig/cron v1.2.0
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.4.0
	github.com/uber-go/tally v3.3.13+incompatible
	github.com/uber/jaeger-client-go v2.15.0+incompatible
	github.com/uber/tchannel-go v1.16.0
	go.uber.org/atomic v1.5.0
	go.uber.org/cadence v0.10.1
	go.uber.org/fx v1.10.0 // indirect
	go.uber.org/goleak v0.10.0
	go.uber.org/multierr v1.4.0
	go.uber.org/net/metrics v1.2.0 // indirect
	go.uber.org/thriftrw v1.20.2
	go.uber.org/yarpc v1.42.1
	go.uber.org/zap v1.13.0
	golang.org/x/net v0.0.0-20190620200207-3b0461eec859
	golang.org/x/time v0.0.0-20170927054726-6dc17368e09b
	google.golang.org/grpc v1.25.1 // indirect
)

replace github.com/temporalio/temporal-proto => ./.gen/proto
