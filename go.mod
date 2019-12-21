module go.temporal.io/temporal

go 1.12

require (
	github.com/codahale/hdrhistogram v0.0.0-20161010025455-3a0bb77429bd // indirect
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a
	github.com/gogo/protobuf v1.3.1
	github.com/gogo/status v1.1.0
	github.com/golang/mock v1.3.1
	github.com/opentracing/opentracing-go v1.1.0
	github.com/pborman/uuid v0.0.0-20160209185913-a97ce2ca70fa
	github.com/robfig/cron v1.2.0
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.4.0
	github.com/temporalio/temporal-proto v0.0.0-00010101000000-000000000000
	github.com/uber-go/atomic v1.4.0 // indirect
	github.com/uber-go/tally v3.3.13+incompatible
	github.com/uber/jaeger-client-go v2.15.0+incompatible
	github.com/uber/jaeger-lib v1.5.0 // indirect
	go.uber.org/atomic v1.5.0
	go.uber.org/goleak v0.10.0
	go.uber.org/zap v1.13.0
	golang.org/x/net v0.0.0-20190620200207-3b0461eec859
	golang.org/x/time v0.0.0-20170927054726-6dc17368e09b
	google.golang.org/grpc v1.26.0
)

replace github.com/temporalio/temporal-proto => ./.gen/proto
