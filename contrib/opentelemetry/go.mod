module go.temporal.io/sdk/contrib/opentelemetry

go 1.16

require (
	github.com/stretchr/testify v1.8.4
	go.opentelemetry.io/otel v1.2.0
	go.opentelemetry.io/otel/sdk v1.2.0
	go.opentelemetry.io/otel/trace v1.2.0
	go.temporal.io/sdk v1.12.0
)

replace go.temporal.io/sdk => ../../
