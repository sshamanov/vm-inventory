# Build stage
FROM golang:1.22-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -ldflags="-s -w" -o /out/inventory-backend ./cmd/inventory-backend/

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=build /out/inventory-backend /usr/bin/inventory-backend

# Copy static frontend assets
COPY web/ /usr/share/inventory-backend/web/

EXPOSE 8080

ENV PROMETHEUS_URL=http://prometheus:9090

ENTRYPOINT ["/usr/bin/inventory-backend"]
