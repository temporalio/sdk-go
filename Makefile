PROJECT_ROOT = code.uber.internal/devexp/minions-client-go.git

# define the list of thrift files the service depends on
# (if you have some)
THRIFT_DIR = idl/code.uber.internal/devexp/minions

THRIFT_SRCS = $(THRIFT_DIR)/minions.thrift

# list all executables
PROGS = example

example: example.go \
	$(wildcard *.go) \
	$(wildcard config/*.go) \
	$(wildcard common/**/*.go) \
	$(wildcard client/flow/*.go) \


-include go-build/rules.mk

go-build/rules.mk:
	git submodule update --init
