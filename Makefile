.PHONY: test bins clean cover cover-ci check errcheck staticcheck lint fmt

# default target
default: check test

# general build-product folder, cleaned as part of `make clean`
BUILD := .build

TEST_TIMEOUT := 5m
TEST_ARG ?= -race -v -timeout $(TEST_TIMEOUT)

INTEG_TEST_ROOT := ./test/
COVER_ROOT := $(abspath $(BUILD)/coverage)
UT_COVER_FILE := $(COVER_ROOT)/unit_test_cover.out
INTEG_ZERO_CACHE_COVER_FILE := $(COVER_ROOT)/integ_test_zero_cache_cover.out
INTEG_NORMAL_CACHE_COVER_FILE := $(COVER_ROOT)/integ_test_normal_cache_cover.out

# Automatically gather all srcs
ALL_SRC :=  $(shell find . -name "*.go")

MOD_DIRS := $(sort $(dir $(shell find . -name go.mod)))
UT_DIRS := $(filter-out $(INTEG_TEST_ROOT)%, $(sort $(dir $(filter %_test.go,$(ALL_SRC)))))
INTEG_TEST_DIRS := $(sort $(dir $(shell find $(INTEG_TEST_ROOT) -name *_test.go)))

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
		(cd "$$dir" && go test . $(TEST_ARG) -coverprofile=$(COVER_ROOT)/"$$dir"/cover.out) || exit 1; \
		cat $(COVER_ROOT)/"$$dir"/cover.out | grep -v "mode: atomic" >> $(UT_COVER_FILE); \
	done;

integration-test-zero-cache: $(BUILD)/dummy
	@mkdir -p $(COVER_ROOT)
	@for dir in $(INTEG_TEST_DIRS); do \
		(cd "$$dir" &&WORKFLOW_CACHE_SIZE=0 go test $(TEST_ARG) . -coverprofile=$(INTEG_ZERO_CACHE_COVER_FILE) -coverpkg=./...) || exit 1; \
	done;

integration-test-normal-cache: $(BUILD)/dummy
	@mkdir -p $(COVER_ROOT)
	@for dir in $(INTEG_TEST_DIRS); do \
		(cd "$$dir" && go test $(TEST_ARG) . -coverprofile=$(INTEG_NORMAL_CACHE_COVER_FILE) -coverpkg=./...) || exit 1; \
	done;

test: unit-test integration-test-zero-cache integration-test-normal-cache

merge-coverage:
	@echo "mode: atomic"
	@grep -hsv "^mode: \w\+" $(COVER_ROOT)/*_cover.out | grep -v ".gen" || true

vet: $(ALL_SRC)
	@for dir in $(MOD_DIRS); do \
		(cd "$$dir" && echo "In $$dir" && go vet ./...) || exit 1; \
	done;

staticcheck: $(ALL_SRC)
	go install honnef.co/go/tools/cmd/staticcheck@v0.4.1
	@for dir in $(MOD_DIRS); do \
		(cd "$$dir" && echo "In $$dir" && staticcheck ./...) || exit 1; \
	done;

errcheck: $(ALL_SRC)
	GO111MODULE=off go get -u github.com/kisielk/errcheck
	@for dir in $(MOD_DIRS); do \
		(cd "$$dir" && echo "In $$dir" && errcheck ./...) || exit 1; \
	done;

update-go-sum: $(ALL_SRC)
	@for dir in $(MOD_DIRS); do \
		(cd "$$dir" && echo "In $$dir" && go get ./...) || exit 1; \
	done;

fmt:
	@gofmt -w $(ALL_SRC)

clean:
	rm -rf $(BUILD)

check: vet errcheck staticcheck copyright bins
