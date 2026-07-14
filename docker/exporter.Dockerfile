# Build stage
FROM golang:1.22-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Static build: Linux AMD64, no CGO
ARG CGO_ENABLED=0
ARG GOOS=linux
ARG GOARCH=amd64

RUN go build -ldflags="-s -w" -o /out/inventory-exporter ./cmd/inventory-exporter/

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=build /out/inventory-exporter /usr/bin/inventory-exporter

EXPOSE 9101

ENTRYPOINT ["/usr/bin/inventory-exporter"]
CMD ["--config.file=/etc/inventory-exporter/config.yaml"]
