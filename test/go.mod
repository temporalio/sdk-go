module go.temporal.io/sdk/test

go 1.24.0

require (
	github.com/golang/mock v1.6.0
	github.com/google/go-cmp v0.7.0
	github.com/google/uuid v1.6.0
	github.com/nexus-rpc/sdk-go v0.5.1
	github.com/opentracing/opentracing-go v1.2.0
	github.com/stretchr/testify v1.11.1
	github.com/uber-go/tally/v4 v4.1.17
	go.opentelemetry.io/otel v1.38.0
	go.opentelemetry.io/otel/sdk v1.38.0
	go.opentelemetry.io/otel/trace v1.38.0
	go.temporal.io/api v1.57.0
	go.temporal.io/sdk v1.37.0
	go.temporal.io/sdk/contrib/opentelemetry v0.6.0
	go.temporal.io/sdk/contrib/opentracing v0.2.0
	go.temporal.io/sdk/contrib/resourcetuner v0.0.0-20251107005034-a451bef0b7c9
	go.temporal.io/sdk/contrib/tally v0.2.0
	go.uber.org/goleak v1.3.0
	google.golang.org/grpc v1.76.0
	google.golang.org/protobuf v1.36.10
)

require (
	github.com/cilium/ebpf v0.20.0 // indirect
	github.com/containerd/cgroups/v3 v3.1.1 // indirect
	github.com/containerd/log v0.1.0 // indirect
	github.com/coreos/go-systemd/v22 v22.6.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/ebitengine/purego v0.9.1 // indirect
	github.com/facebookgo/clock v0.0.0-20150410010913-600d898af40a // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/grpc-ecosystem/go-grpc-middleware/v2 v2.3.3 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.3 // indirect
	github.com/lufia/plan9stats v0.0.0-20251013123823-9fd1530e3ec3 // indirect
	github.com/opencontainers/runtime-spec v1.3.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/power-devops/perfstat v0.0.0-20240221224432-82ca36839d55 // indirect
	github.com/robfig/cron v1.2.0 // indirect
	github.com/shirou/gopsutil/v4 v4.25.10 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/stretchr/objx v0.5.3 // indirect
	github.com/tklauser/go-sysconf v0.3.16 // indirect
	github.com/tklauser/numcpus v0.11.0 // indirect
	github.com/twmb/murmur3 v1.1.8 // indirect
	github.com/yusufpapurcu/wmi v1.2.4 // indirect
	go.einride.tech/pid v0.2.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/net v0.47.0 // indirect
	golang.org/x/sync v0.18.0 // indirect
	golang.org/x/sys v0.38.0 // indirect
	golang.org/x/text v0.31.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20251111163417-95abcf5c77ba // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251111163417-95abcf5c77ba // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace (
	github.com/nexus-rpc/sdk-go => ../../nexus-sdk-go
	go.temporal.io/api => ../../temporal-api-go
	go.temporal.io/sdk => ../
	go.temporal.io/sdk/contrib/opentelemetry => ../contrib/opentelemetry
	go.temporal.io/sdk/contrib/opentracing => ../contrib/opentracing
	go.temporal.io/sdk/contrib/resourcetuner => ../contrib/resourcetuner
	go.temporal.io/sdk/contrib/tally => ../contrib/tally
)
