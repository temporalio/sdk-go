module go.temporal.io/sdk/test

go 1.16

require (
	github.com/golang/mock v1.6.0
	github.com/google/uuid v1.3.0
	github.com/opentracing/opentracing-go v1.2.0
	github.com/pborman/uuid v1.2.1
	github.com/stretchr/testify v1.8.4
	github.com/uber-go/tally/v4 v4.1.1
	go.opentelemetry.io/otel/sdk v1.2.0
	go.opentelemetry.io/otel/trace v1.2.0
	go.temporal.io/api v1.24.0
	go.temporal.io/sdk v1.12.0
	go.temporal.io/sdk/contrib/opentelemetry v0.1.0
	go.temporal.io/sdk/contrib/opentracing v0.0.0-00010101000000-000000000000
	go.temporal.io/sdk/contrib/tally v0.1.0
	go.uber.org/goleak v1.1.11
	google.golang.org/grpc v1.58.3
	google.golang.org/protobuf v1.31.0
)

replace (
	go.temporal.io/api => github.com/tdeebswihart/temporal-api-go v0.0.0-20231016220718-646941139bf7
	go.temporal.io/sdk => ../
	go.temporal.io/sdk/contrib/opentelemetry => ../contrib/opentelemetry
	go.temporal.io/sdk/contrib/opentracing => ../contrib/opentracing
	go.temporal.io/sdk/contrib/tally => ../contrib/tally
)
