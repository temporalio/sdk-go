PROJECT_ROOT = code.uber.internal/devexp/cadence-client-go.git

# define the list of thrift files the service depends on
# (if you have some)
THRIFT_DIR = idl/github.com/uber/cadence

THRIFT_SRCS = $(THRIFT_DIR)/cadence.thrift \
	$(THRIFT_DIR)/shared.thrift

# list all executables
PROGS = cmd/samples/sample \

cmd/samples/sample: cmd/samples/*.go \
	$(wildcard cmd/samples/**/*.go) \
	$(wildcard config/*.go) \
	$(wildcard common/*.go) \
	$(wildcard common/**/*.go) \
	$(wildcard client/cadence/*.go) \

-include go-build/rules.mk

go-build/rules.mk:
	git submodule update --init
