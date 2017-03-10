PROJECT_ROOT = code.uber.internal/devexp/minions-client-go.git

# define the list of thrift files the service depends on
# (if you have some)
THRIFT_DIR = idl/github.com/uber/cadence

THRIFT_SRCS = $(THRIFT_DIR)/cadence.thrift \
	$(THRIFT_DIR)/shared.thrift

# list all executables
PROGS = cmd/samples/greetings/sample \
        cmd/samples/helloworld/sample \

cmd/samples/greetings/sample: cmd/samples/greetings/*.go \
	$(wildcard config/*.go) \
	$(wildcard common/*.go) \
	$(wildcard common/**/*.go) \
	$(wildcard client/cadence/*.go) \

cmd/samples/helloworld/sample: cmd/samples/helloworld/sample.go \

-include go-build/rules.mk

go-build/rules.mk:
	git submodule update --init
