# Binary-only image — run `make build-exporter` first, then:
#   docker build -f docker/bin.Dockerfile --build-arg BINARY=inventory-exporter -t exporter-bin .
#   docker create --name tmp exporter-bin
#   docker cp tmp:/binary ./inventory-exporter
#   docker rm tmp

FROM scratch
ARG BINARY=inventory-exporter
COPY bin/${BINARY} /binary
