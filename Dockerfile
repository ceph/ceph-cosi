FROM gcr.io/distroless/static:latest
LABEL maintainers="Ceph COSI Authors"
LABEL description="Ceph COSI driver"

COPY ./cmd/ceph-cosi-driver/ceph-cosi-driver ceph-cosi-driver
ENTRYPOINT ["/ceph-cosi-driver"]
