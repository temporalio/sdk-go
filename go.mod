module go.temporal.io/temporal

go 1.12

require (
	github.com/anmitsu/go-shlex v0.0.0-20161002113705-648efa622239 // indirect
	github.com/apache/thrift v0.0.0-20161221203622-b2a4d4ae21c7
	github.com/bmizerany/perks v0.0.0-20141205001514-d9a9656a3a4b // indirect
	github.com/codahale/hdrhistogram v0.0.0-20161010025455-3a0bb77429bd // indirect
	github.com/crossdock/crossdock-go v0.0.0-20160816171116-049aabb0122b // indirect
	github.com/davecgh/go-spew v1.1.1
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a
	github.com/fatih/structtag v1.0.0 // indirect
	github.com/flynn/go-shlex v0.0.0-20150515145356-3f9db97f8568 // indirect
	github.com/golang/mock v1.3.1
	github.com/golang/protobuf v1.3.2
	github.com/jessevdk/go-flags v1.4.0 // indirect
	github.com/opentracing/opentracing-go v1.1.0
	github.com/pborman/uuid v0.0.0-20160209185913-a97ce2ca70fa
	github.com/prashantv/protectmem v0.0.0-20171002184600-e20412882b3a // indirect
	github.com/robfig/cron v1.2.0
	github.com/samuel/go-thrift v0.0.0-20190219015601-e8b6b52668fe // indirect
	github.com/sirupsen/logrus v1.4.2
	github.com/streadway/quantile v0.0.0-20150917103942-b0c588724d25 // indirect
	github.com/stretchr/testify v1.4.0
	github.com/temporalio/temporal-proto v0.0.0
	github.com/uber-go/atomic v1.4.0 // indirect
	github.com/uber-go/mapdecode v1.0.0 // indirect
	github.com/uber-go/tally v3.3.13+incompatible
	github.com/uber/jaeger-client-go v2.15.0+incompatible
	github.com/uber/jaeger-lib v1.5.0 // indirect
	github.com/uber/tchannel-go v1.16.0
	go.uber.org/atomic v1.5.0
	go.uber.org/goleak v0.10.0
	go.uber.org/multierr v1.4.0
	go.uber.org/thriftrw v1.20.2
	go.uber.org/yarpc v1.42.1
	go.uber.org/zap v1.13.0
	golang.org/x/net v0.0.0-20190620200207-3b0461eec859
	golang.org/x/time v0.0.0-20170927054726-6dc17368e09b
	google.golang.org/grpc v1.25.1
)

replace github.com/temporalio/temporal-proto => ./.gen/proto
