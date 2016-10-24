PROJECT_ROOT = code.uber.internal/devexp/minions-client-go

# define the list of thrift files the service depends on
# (if you have some)
THRIFT_SRCS = idl/code.uber.internal/devexp/minions-client-go/minions_client_go.thrift

# list all executables
PROGS = minions-client-go

minions-client-go: $(wildcard *.go)

-include go-build/rules.mk

go-build/rules.mk:
	git submodule update --init
