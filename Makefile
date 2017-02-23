PROJECT_ROOT = code.uber.internal/devexp/minions-client-go.git

# define the list of thrift files the service depends on
# (if you have some)
THRIFT_DIR = idl/github.com/uber/cadence

THRIFT_SRCS = $(THRIFT_DIR)/cadence.thrift \
	$(THRIFT_DIR)/shared.thrift

# list all executables
PROGS = cmd/example/example

cmd/example/example: cmd/example/*.go \
	$(wildcard config/*.go) \
	$(wildcard common/*.go) \
	$(wildcard common/**/*.go) \
	$(wildcard client/flow/*.go) \
	$(wildcard examples/*.go) \


-include go-build/rules.mk

go-build/rules.mk:
	git submodule update --init
