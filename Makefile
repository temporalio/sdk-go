.PHONY: test bins clean cover cover_ci

# default target
default: test

IMPORT_ROOT := go.temporal.io/temporal

# general build-product folder, cleaned as part of `make clean`
BUILD := .build

INTEG_TEST_ROOT := ./test
COVER_ROOT := $(BUILD)/coverage
UT_COVER_FILE := $(COVER_ROOT)/unit_test_cover.out
INTEG_STICKY_OFF_COVER_FILE := $(COVER_ROOT)/integ_test_sticky_off_cover.out
INTEG_STICKY_ON_COVER_FILE := $(COVER_ROOT)/integ_test_sticky_on_cover.out

# Automatically gather all srcs
ALL_SRC :=  $(shell find . -name "*.go" | grep -v -e .gen/ -e .build/)

UT_DIRS := $(filter-out $(INTEG_TEST_ROOT)%, $(sort $(dir $(filter %_test.go,$(ALL_SRC)))))
INTEG_TEST_DIRS := $(sort $(dir $(shell find $(INTEG_TEST_ROOT) -name *_test.go)))

# Files that needs to run lint. Excludes testify mocks.
LINT_SRC := $(filter-out ./mocks/%,$(ALL_SRC))

#================================= protobuf ===================================
PROTO_ROOT := proto
PROTO_REPO := github.com/temporalio/temporal-proto
# List only subdirectories with *.proto files (sort to remove duplicates).
# Note: using "shell find" instead of "wildcard" because "wildcard" caches directory structure.
PROTO_DIRS = $(sort $(dir $(shell find $(PROTO_ROOT) -name "*.proto")))
PROTO_SERVICES := $(shell find $(PROTO_ROOT) -name "*service.proto")
PROTO_GEN = .gen/proto

# Everything that deals with go modules (go.mod) needs to take dependency on this target.
$(PROTO_ROOT)/go.mod:
	cd $(PROTO_ROOT) && go mod init $(PROTO_REPO)

clean-proto:
	$(foreach PROTO_DIR,$(PROTO_DIRS),rm -f $(PROTO_DIR)*.go;)
	rm -rf $(PROTO_ROOT)/*mock

update-proto-submodule:
	git submodule update --remote $(PROTO_ROOT)

install-proto-submodule:
	git submodule update --init $(PROTO_ROOT)

protoc:
#   run protoc separately for each directory because of different package names
	$(foreach PROTO_DIR,$(PROTO_DIRS),protoc --proto_path=$(PROTO_ROOT) --gogoslick_out=paths=source_relative:$(PROTO_ROOT) $(PROTO_DIR)*.proto;)
	$(foreach PROTO_SERVICE,$(PROTO_SERVICES),protoc --proto_path=$(PROTO_ROOT) --yarpc-go_out=$(PROTO_ROOT) $(PROTO_SERVICE);)

# All YARPC generated service files pathes relative to PROTO_ROOT
PROTO_YARPC_SERVICES = $(patsubst $(PROTO_ROOT)/%,%,$(shell find $(PROTO_ROOT) -name "service.pb.yarpc.go"))
dir_no_slash = $(patsubst %/,%,$(dir $(1)))
dirname = $(notdir $(call dir_no_slash,$(1)))

proto-mock: $(PROTO_ROOT)/go.mod tools-install
	@echo "Generate proto mocks..."
	@$(foreach PROTO_YARPC_SERVICE,$(PROTO_YARPC_SERVICES),cd $(PROTO_ROOT) && mockgen -package $(call dirname,$(PROTO_YARPC_SERVICE))mock -source $(PROTO_YARPC_SERVICE) -destination $(call dir_no_slash,$(PROTO_YARPC_SERVICE))mock/$(notdir $(PROTO_YARPC_SERVICE:go=mock.go)) )

update-proto: clean-proto update-proto-submodule tools-install protoc proto-mock

proto: clean-proto install-proto-submodule tools-install protoc proto-mock
#==============================================================================

tools-install: $(PROTO_ROOT)/go.mod
	GO111MODULE=off go get -u github.com/myitcv/gobin
	GO111MODULE=off go get -u github.com/gogo/protobuf/protoc-gen-gogoslick
	GO111MODULE=off go get -u go.uber.org/yarpc/encoding/protobuf/protoc-gen-yarpc-go
	GOOS= GOARCH= gobin -mod=readonly golang.org/x/lint/golint
	GOOS= GOARCH= gobin -mod=readonly github.com/golang/mock/mockgen
	GOOS= GOARCH= gobin -mod=readonly honnef.co/go/tools/cmd/staticcheck
	GOOS= GOARCH= gobin -mod=readonly github.com/kisielk/errcheck

# `make copyright` or depend on "copyright" to force-run licensegen,
# or depend on $(BUILD)/copyright to let it run as needed.
copyright $(BUILD)/copyright: $(ALL_SRC)
	@mkdir -p $(BUILD)
	go run ./internal/cmd/tools/copyright/licensegen.go --verifyOnly
	@touch $(BUILD)/copyright

$(BUILD)/dummy:
	go build -i -o $@ internal/cmd/dummy/dummy.go

bins: $(ALL_SRC) proto $(BUILD)/copyright lint $(BUILD)/dummy

unit_test: $(BUILD)/dummy
	@mkdir -p $(COVER_ROOT)
	@echo "mode: atomic" > $(UT_COVER_FILE)
	@for dir in $(UT_DIRS); do \
		mkdir -p $(COVER_ROOT)/"$$dir"; \
		go test "$$dir" $(TEST_ARG) -coverprofile=$(COVER_ROOT)/"$$dir"/cover.out || exit 1; \
		cat $(COVER_ROOT)/"$$dir"/cover.out | grep -v "mode: atomic" >> $(UT_COVER_FILE); \
	done;

integ_test_sticky_off: $(BUILD)/dummy
	@mkdir -p $(COVER_ROOT)
	@for dir in $(INTEG_TEST_DIRS); do \
		STICKY_OFF=true go test $(TEST_ARG) "$$dir" -coverprofile=$(INTEG_STICKY_OFF_COVER_FILE) -coverpkg=./... || exit 1; \
	done;

integ_test_sticky_on: $(BUILD)/dummy
	@mkdir -p $(COVER_ROOT)
	@for dir in $(INTEG_TEST_DIRS); do \
		STICKY_OFF=false go test $(TEST_ARG) "$$dir" -coverprofile=$(INTEG_STICKY_ON_COVER_FILE) -coverpkg=./... || exit 1; \
	done;

test: unit_test integ_test_sticky_off integ_test_sticky_on

$(COVER_ROOT)/cover.out: $(UT_COVER_FILE) $(INTEG_STICKY_OFF_COVER_FILE) $(INTEG_STICKY_ON_COVER_FILE)
	@echo "mode: atomic" > $(COVER_ROOT)/cover.out
	cat $(UT_COVER_FILE) | grep -v "mode: atomic" | grep -v ".gen" >> $(COVER_ROOT)/cover.out
	cat $(INTEG_STICKY_OFF_COVER_FILE) | grep -v "mode: atomic" | grep -v ".gen" >> $(COVER_ROOT)/cover.out
	cat $(INTEG_STICKY_ON_COVER_FILE) | grep -v "mode: atomic" | grep -v ".gen" >> $(COVER_ROOT)/cover.out

cover: $(COVER_ROOT)/cover.out
	go tool cover -html=$(COVER_ROOT)/cover.out;

cover_ci: $(COVER_ROOT)/cover.out
	goveralls -coverprofile=$(COVER_ROOT)/cover.out -service=buildkite || echo -e "\x1b[31mCoveralls failed\x1b[m";

# golint fails to report many lint failures if it is only given a single file
# to work on at a time, and it can't handle multiple packages at once, *and*
# we can't exclude files from its checks, so for best results we need to give
# it a whitelist of every file in every package that we want linted, per package.
#
# so lint + this golint func works like:
# - iterate over all lintable dirs (outputs "./folder/")
# - find .go files in a dir (via wildcard, so not recursively)
# - filter to only files in LINT_SRC
# - if it's not empty, run golint against the list
define lint_if_present
test -n "$1" && golint -set_exit_status $1
endef

lint: tools-install $(ALL_SRC)
	$(foreach pkg,\
		$(sort $(dir $(LINT_SRC))), \
		$(call lint_if_present,$(filter $(wildcard $(pkg)*.go),$(LINT_SRC))) || ERR=1; \
	) test -z "$$ERR" || exit 1
	@OUTPUT=`gofmt -l $(ALL_SRC) 2>&1`; \
	if [ "$$OUTPUT" ]; then \
		echo "Run 'make fmt'. gofmt must be run on the following files:"; \
		echo "$$OUTPUT"; \
		exit 1; \
	fi

staticcheck: tools-install $(ALL_SRC)
	staticcheck ./...

errcheck: tools-install $(ALL_SRC)
	errcheck ./...

fmt:
	@gofmt -w $(ALL_SRC)

clean: clean-proto
	rm -rf $(BUILD)
