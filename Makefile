.PHONY: test bins clean cover cover-ci check errcheck staticcheck lint fmt

# default target
default: check test

IMPORT_ROOT := go.temporal.io/temporal

# general build-product folder, cleaned as part of `make clean`
BUILD := .build

INTEG_TEST_ROOT := ./test
COVER_ROOT := $(BUILD)/coverage
UT_COVER_FILE := $(COVER_ROOT)/unit_test_cover.out
INTEG_STICKY_OFF_COVER_FILE := $(COVER_ROOT)/integ_test_sticky_off_cover.out
INTEG_STICKY_ON_COVER_FILE := $(COVER_ROOT)/integ_test_sticky_on_cover.out

# Automatically gather all srcs
ALL_SRC :=  $(shell find . -name "*.go")

UT_DIRS := $(filter-out $(INTEG_TEST_ROOT)%, $(sort $(dir $(filter %_test.go,$(ALL_SRC)))))
INTEG_TEST_DIRS := $(sort $(dir $(shell find $(INTEG_TEST_ROOT) -name *_test.go)))

# Files that needs to run lint. Excludes testify mocks.
LINT_SRC := $(filter-out ./mocks/%,$(ALL_SRC))

# `make copyright` or depend on "copyright" to force-run licensegen,
# or depend on $(BUILD)/copyright to let it run as needed.
copyright $(BUILD)/copyright:
	go run ./internal/cmd/tools/copyright/licensegen.go --verifyOnly
	@mkdir -p $(BUILD)
	@touch $(BUILD)/copyright

$(BUILD)/dummy:
	go build -o $@ internal/cmd/dummy/dummy.go

bins: $(BUILD)/copyright $(BUILD)/dummy

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
	cat $(UT_COVER_FILE) | grep -v "^mode: \w\+" | grep -v ".gen" >> $(COVER_ROOT)/cover.out
	cat $(INTEG_STICKY_OFF_COVER_FILE) | grep -v "^mode: \w\+" | grep -v ".gen" >> $(COVER_ROOT)/cover.out
	cat $(INTEG_STICKY_ON_COVER_FILE) | grep -v "^mode: \w\+" | grep -v ".gen" >> $(COVER_ROOT)/cover.out

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

lint: $(ALL_SRC)
	GO111MODULE=off go get -u golang.org/x/lint/golint
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

staticcheck: $(ALL_SRC)
	GO111MODULE=off go get -u honnef.co/go/tools/cmd/staticcheck
	staticcheck ./...

errcheck: $(ALL_SRC)
	GO111MODULE=off go get -u github.com/kisielk/errcheck
	errcheck ./...

fmt:
	@gofmt -w $(ALL_SRC)

clean:
	rm -rf $(BUILD)

check: lint errcheck staticcheck copyright bins