.PHONY: test bins clean cover cover-ci check errcheck staticcheck lint fmt

# default target
default: check test

# general build-product folder, cleaned as part of `make clean`
BUILD := .build

TEST_TIMEOUT := 3m
TEST_ARG ?= -race -v -timeout $(TEST_TIMEOUT)

INTEG_TEST_ROOT := ./test
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

# Ensure generated code dependent on the API is not stale
generatorcheck:
	(cd converter && go run ../internal/cmd/generateinterceptor/main.go -verifyOnly)
	(cd client && go run ../internal/cmd/generateproxy/main.go -verifyOnly)

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

$(COVER_ROOT)/cover.out: $(UT_COVER_FILE) $(INTEG_ZERO_CACHE_COVER_FILE) $(INTEG_NORMAL_CACHE_COVER_FILE)
	@echo "mode: atomic" > $(COVER_ROOT)/cover.out
	cat $(UT_COVER_FILE) | grep -v "^mode: \w\+" | grep -v ".gen" >> $(COVER_ROOT)/cover.out
	cat $(INTEG_ZERO_CACHE_COVER_FILE) | grep -v "^mode: \w\+" | grep -v ".gen" >> $(COVER_ROOT)/cover.out
	cat $(INTEG_NORMAL_CACHE_COVER_FILE) | grep -v "^mode: \w\+" | grep -v ".gen" >> $(COVER_ROOT)/cover.out

cover: $(COVER_ROOT)/cover.out
	go tool cover -html=$(COVER_ROOT)/cover.out;

cover_ci: $(COVER_ROOT)/cover.out
	goveralls -coverprofile=$(COVER_ROOT)/cover.out -service=buildkite || echo -e "\x1b[31mCoveralls failed\x1b[m";

vet: $(ALL_SRC)
	@for dir in $(MOD_DIRS); do \
		(cd "$$dir" && go vet ./...) || exit 1; \
	done;

staticcheck: $(ALL_SRC)
	go install honnef.co/go/tools/cmd/staticcheck@latest
	@for dir in $(MOD_DIRS); do \
		(cd "$$dir" && staticcheck ./...) || exit 1; \
	done;

errcheck: $(ALL_SRC)
	GO111MODULE=off go get -u github.com/kisielk/errcheck
	@for dir in $(MOD_DIRS); do \
		(cd "$$dir" && errcheck ./...) || exit 1; \
	done;

fmt:
	@gofmt -w $(ALL_SRC)

clean:
	rm -rf $(BUILD)

check: vet errcheck staticcheck copyright generatorcheck bins

##### Fossa #####
fossa-install: 
	curl -H 'Cache-Control: no-cache' https://raw.githubusercontent.com/fossas/fossa-cli/master/install.sh | bash

fossa-init:
	fossa init --include-all --no-ansi

fossa-analyze:
	fossa analyze --no-ansi -b $${BUILDKITE_BRANCH:-$$(git branch --show-current)}	

fossa-test:
	fossa test --timeout 1800 --no-ansi

