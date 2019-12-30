.PHONY: test bins clean cover cover-ci check errcheck staticcheck lint fmt

# default target
default: check

IMPORT_ROOT := go.temporal.io/temporal

# general build-product folder, cleaned as part of `make clean`
BUILD := .build

PROTO_ROOT := proto
PROTO_GEN := .gen/proto
PROTO_REPO := github.com/temporalio/temporal-proto

PROTO_DIRS := common enums errordetails workflowservice
PROTO_SERVICES := workflowservice

INTEG_TEST_ROOT := ./test
COVER_ROOT := $(BUILD)/coverage
UT_COVER_FILE := $(COVER_ROOT)/unit-test_cover.out
INTEG_STICKY_OFF_COVER_FILE := $(COVER_ROOT)/integration-test-sticky-off_cover.out
INTEG_STICKY_ON_COVER_FILE := $(COVER_ROOT)/integration-test-sticky-on_cover.out

# Automatically gather all srcs
ALL_SRC :=  $(shell find . -name "*.go" | grep -v -e $(PROTO_GEN))

UT_DIRS := $(filter-out $(INTEG_TEST_ROOT)%, $(sort $(dir $(filter %_test.go,$(ALL_SRC)))))
INTEG_TEST_DIRS := $(sort $(dir $(shell find $(INTEG_TEST_ROOT) -name *_test.go)))

# Files that needs to run lint. Excludes testify mocks.
LINT_SRC := $(filter-out ./mocks/%,$(ALL_SRC))

$(PROTO_GEN):
	mkdir -p $(PROTO_GEN)
	cd $(PROTO_GEN) && go mod init $(PROTO_REPO)

clean-proto:
	rm -rf $(PROTO_GEN)/*/

update-proto-submodule:
	git submodule update --init --remote $(PROTO_ROOT)

proto-plugins:
	GO111MODULE=off go get -u github.com/gogo/protobuf/protoc-gen-gogoslick
	GO111MODULE=off go get -u google.golang.org/grpc

protoc:
#   run protoc separately for each directory because of different package names
	$(foreach PROTO_DIR,$(PROTO_DIRS),protoc --proto_path=$(PROTO_ROOT) --gogoslick_out=plugins=grpc,paths=source_relative:$(PROTO_GEN) $(PROTO_ROOT)/$(PROTO_DIR)/*.proto;)

# All gRPC generated service files pathes relative to PROTO_GEN
PROTO_GRPC_SERVICES = $(patsubst $(PROTO_GEN)/%,%,$(shell find $(PROTO_GEN) -name "service.pb.go"))
dir_no_slash = $(patsubst %/,%,$(dir $(1)))
dirname = $(notdir $(call dir_no_slash,$(1)))

proto-mock: protoc gobin
	@echo "Generate proto mocks..."
	gobin -mod=readonly github.com/golang/mock/mockgen
	@$(foreach PROTO_GRPC_SERVICE,$(PROTO_GRPC_SERVICES),cd $(PROTO_GEN) && mockgen -package $(call dirname,$(PROTO_GRPC_SERVICE))mock -source $(PROTO_GRPC_SERVICE) -destination $(call dir_no_slash,$(PROTO_GRPC_SERVICE))mock/$(notdir $(PROTO_GRPC_SERVICE:go=mock.go)) )

update-proto: $(PROTO_GEN) clean-proto update-proto-submodule proto-plugins protoc proto-mock copyright

gobin:
	GO111MODULE=off go get -u github.com/myitcv/gobin

# `make copyright` or depend on "copyright" to force-run licensegen,
# or depend on $(BUILD)/copyright to let it run as needed.
copyright $(BUILD)/copyright: $(ALL_SRC)
	go run ./internal/cmd/tools/copyright/licensegen.go --verifyOnly
	@mkdir -p $(BUILD)
	@touch $(BUILD)/copyright

$(BUILD)/dummy:
	go build -i -o $@ internal/cmd/dummy/dummy.go

bins: $(ALL_SRC) $(BUILD)/copyright lint $(BUILD)/dummy

unit-test: $(BUILD)/dummy
	@mkdir -p $(COVER_ROOT)
	@echo "mode: atomic" > $(UT_COVER_FILE)
	@for dir in $(UT_DIRS); do \
		mkdir -p $(COVER_ROOT)/"$$dir"; \
		go test "$$dir" $(TEST_ARG) -coverprofile=$(COVER_ROOT)/"$$dir"/cover.out || exit 1; \
		cat $(COVER_ROOT)/"$$dir"/cover.out | grep -v "mode: atomic" >> $(UT_COVER_FILE); \
	done;

integration-test-sticky-off: $(BUILD)/dummy
	@mkdir -p $(COVER_ROOT)
	@for dir in $(INTEG_TEST_DIRS); do \
		STICKY_OFF=true go test $(TEST_ARG) "$$dir" -coverprofile=$(INTEG_STICKY_OFF_COVER_FILE) -coverpkg=./... || exit 1; \
	done;

integration-test-sticky-on: $(BUILD)/dummy
	@mkdir -p $(COVER_ROOT)
	@for dir in $(INTEG_TEST_DIRS); do \
		STICKY_OFF=false go test $(TEST_ARG) "$$dir" -coverprofile=$(INTEG_STICKY_ON_COVER_FILE) -coverpkg=./... || exit 1; \
	done;

test: unit-test integration-test-sticky-off integration-test-sticky-on

$(COVER_ROOT)/cover.out: $(UT_COVER_FILE) $(INTEG_STICKY_OFF_COVER_FILE) $(INTEG_STICKY_ON_COVER_FILE)
	@echo "mode: atomic" > $(COVER_ROOT)/cover.out
	cat $(UT_COVER_FILE) | grep -v "mode: atomic" | grep -v ".gen" >> $(COVER_ROOT)/cover.out
	cat $(INTEG_STICKY_OFF_COVER_FILE) | grep -v "mode: atomic" | grep -v ".gen" >> $(COVER_ROOT)/cover.out
	cat $(INTEG_STICKY_ON_COVER_FILE) | grep -v "mode: atomic" | grep -v ".gen" >> $(COVER_ROOT)/cover.out

cover: $(COVER_ROOT)/cover.out
	go tool cover -html=$(COVER_ROOT)/cover.out;

cover-ci: $(COVER_ROOT)/cover.out
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

lint: gobin $(ALL_SRC)
	gobin -mod=readonly golang.org/x/lint/golint
	@$(foreach pkg,\
		$(sort $(dir $(LINT_SRC))), \
		$(call lint_if_present,$(filter $(wildcard $(pkg)*.go),$(LINT_SRC))) || ERR=1; \
	) test -z "$$ERR" || exit 1
	@OUTPUT=`gofmt -l $(ALL_SRC) 2>&1`; \
	if [ "$$OUTPUT" ]; then \
		echo "Run 'make fmt'. gofmt must be run on the following files:"; \
		echo "$$OUTPUT"; \
		exit 1; \
	fi

staticcheck: gobin $(ALL_SRC)
	gobin -mod=readonly -run honnef.co/go/tools/cmd/staticcheck ./...

errcheck: gobin $(ALL_SRC)
	gobin -mod=readonly -run github.com/kisielk/errcheck ./...

fmt:
	@gofmt -w $(ALL_SRC)

clean:
	rm -rf $(BUILD)

check: fmt lint errcheck staticcheck test