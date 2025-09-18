FROM golang:latest AS builder
COPY . /builder
RUN cd /builder && ls -alh && find . && make build
FROM gcr.io/distroless/static:latest
LABEL maintainers="Ceph COSI Authors"
LABEL description="Ceph COSI driver"

COPY --from=builder /builder/bin/ceph-cosi-driver ceph-cosi-driver
ENTRYPOINT ["/ceph-cosi-driver"]