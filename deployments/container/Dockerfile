# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at https://mozilla.org/MPL/2.0/.
#
# Portions of this file were modified from the kubernetes-network-driver-basic
# project, which is licensed under the Apache License, Version 2.0.

ARG GOARCH="amd64"

FROM golang:1.26 AS builder
# golang envs
ARG GOARCH="amd64"
ARG GOOS=linux
ENV CGO_ENABLED=0

WORKDIR /go/src/app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o /go/bin/driver ./cmd/dra-driver-ettus-sdr

FROM debian:12-slim
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y uhd-host iproute2 usbutils && \
    uhd_images_downloader && \
    rm -rf /var/lib/apt/lists/*
COPY --from=builder /go/bin/driver /driver
CMD ["/driver"]
