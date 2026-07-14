# Run `make build-exporter` first to produce bin/inventory-exporter.
# Then: docker build --network host -f docker/exporter.Dockerfile -t inventory-exporter .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY bin/inventory-exporter /usr/bin/inventory-exporter

EXPOSE 9101

ENTRYPOINT ["/usr/bin/inventory-exporter"]
CMD ["--config.file=/etc/inventory-exporter/config.yaml"]
