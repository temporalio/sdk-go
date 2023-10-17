module go.temporal.io/sdk/contrib/opentracing

go 1.16

require (
	github.com/opentracing/opentracing-go v1.2.0
	github.com/stretchr/testify v1.8.4
	go.temporal.io/sdk v1.12.0
)

replace (
	go.temporal.io/api => github.com/tdeebswihart/temporal-api-go v0.0.0-20231016220718-646941139bf7
	go.temporal.io/sdk => ../../
)
