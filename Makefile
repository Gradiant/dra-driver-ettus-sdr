# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Portions of this file were modified from the kubernetes-network-driver-basic
# project, which is licensed under the Apache License, Version 2.0.

REPO_ROOT := $(CURDIR)
OUT_DIR := $(REPO_ROOT)/bin
BINARY_NAME ?= dra-driver-ettus-sdr

# disable CGO by default for static binaries
CGO_ENABLED = 0
export GO111MODULE=on
export CGO_ENABLED

.PHONY: all build clean test lint update image push kind-image

all: build

build:
	go build -v -o "$(OUT_DIR)/$(BINARY_NAME)" ./cmd/dra-driver-ettus-sdr

clean:
	rm -rf "$(OUT_DIR)/"

test:
	CGO_ENABLED=1 go test -v -race -count 1 ./...

lint:
	hack/lint.sh

update:
	go mod tidy

# Default Docker Registry and Image
REGISTRY ?= harbor.gradiant.org/lab5g
IMAGE_NAME ?= dra-driver-ettus-sdr
IMAGE := $(REGISTRY)/$(IMAGE_NAME)

# Tag based on date-sha or fallback to latest
TAG ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "latest")
TAGGED_IMAGE := $(IMAGE):$(TAG)
LATEST_IMAGE := $(IMAGE):latest

image:
	docker build -f deployments/container/Dockerfile --network host -t $(TAGGED_IMAGE) .
	docker tag $(TAGGED_IMAGE) $(LATEST_IMAGE)

push: image
	docker push $(TAGGED_IMAGE)
	docker push $(LATEST_IMAGE)

KIND_CLUSTER_NAME ?= dra
kind-image: image
	kind load docker-image $(LATEST_IMAGE) --name $(KIND_CLUSTER_NAME)
	kubectl delete -f deployments/manifests/install.yaml || true
	kubectl apply -f deployments/manifests/install.yaml
