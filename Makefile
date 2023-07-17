# Copyright 2021 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

CMDS=ceph-cosi-driver

REGISTRY_NAME=quay.io/ceph/cosi
IMAGE_TAGS=latest

all: build

.PHONY: build-% build container-% container clean

# A space-separated list of all commands in the repository, must be
# set in main Makefile of a repository.
# CMDS=

# Revision that gets built into each binary via the main.version
# string. Uses the `git describe` output based on the most recent
# version tag with a short revision suffix or, if nothing has been
# tagged yet, just the revision.
#
# Beware that tags may also be missing in shallow clones as done by
# some CI systems (like TravisCI, which pulls only 50 commits).
REV=$(shell git describe --long --tags --match='v*' --dirty 2>/dev/null || git rev-list -n1 HEAD)

# A space-separated list of image tags under which the current build is to be pushed.
# Determined dynamically.
IMAGE_TAGS=

# A "canary" image gets built if the current commit is the head of the remote "master" branch.
# That branch does not exist when building some other branch in TravisCI.
IMAGE_TAGS+=$(shell if [ "$$(git rev-list -n1 HEAD)" = "$$(git rev-list -n1 origin/master 2>/dev/null)" ]; then echo "canary"; fi)

# A "X.Y.Z-canary" image gets built if the current commit is the head of a "origin/release-X.Y.Z" branch.
# The actual suffix does not matter, only the "release-" prefix is checked.
IMAGE_TAGS+=$(shell git branch -r --points-at=HEAD | grep 'origin/release-' | grep -v -e ' -> ' | sed -e 's;.*/release-\(.*\);\1-canary;')

# A release image "vX.Y.Z" gets built if there is a tag of that format for the current commit.
# --abbrev=0 suppresses long format, only showing the closest tag.
IMAGE_TAGS+=$(shell tagged="$$(git describe --tags --match='v*' --abbrev=0)"; if [ "$$tagged" ] && [ "$$(git rev-list -n1 HEAD)" = "$$(git rev-list -n1 $$tagged)" ]; then echo $$tagged; fi)

# Images are named after the command contained in them.
IMAGE_NAME=$(REGISTRY_NAME)/$*

ARCH := $(if $(GOARCH),$(GOARCH),$(shell go env GOARCH))

# detect container tools, prefer Podman over Docker
CONTAINER_CMD ?= $(shell podman version >/dev/null 2>&1 && echo podman)
ifeq ($(CONTAINER_CMD),)
CONTAINER_CMD = $(shell docker version >/dev/null 2>&1 && echo docker)
endif

# Specific packages can be excluded from each of the tests below by setting the *_FILTER_CMD variables
# to something like "| grep -v 'github.com/kubernetes-csi/project/pkg/foobar'". See usage below.

build-%:
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux go build -a -ldflags '-X main.version=$(REV) -extldflags "-static"' -o ./bin/$* ./cmd/$*
	if [ "$$ARCH" = "amd64" ]; then \
		CGO_ENABLED=0 GOOS=windows go build -a -ldflags '-X main.version=$(REV) -extldflags "-static"' -o ./bin/$*.exe ./cmd/$* ; \
		CGO_ENABLED=0 GOOS=linux GOARCH=ppc64le go build -a -ldflags '-X main.version=$(REV) -extldflags "-static"' -o ./bin/$*-ppc64le ./cmd/$* ; \
	fi

container-%: build-%
	$(CONTAINER_CMD) build -t $*:latest -f $(shell if [ -e ./cmd/$*/Dockerfile ]; then echo ./cmd/$*/Dockerfile; else echo Dockerfile; fi) --label revision=$(REV) .

build: $(CMDS:%=build-%)
container: $(CMDS:%=container-%)

clean:
	-rm -rf bin

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: ## Run unit tests against code.
	go test ./... -coverprofile=coverage.txt -covermode=atomic
