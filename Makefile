.PHONY: all build build-exporter build-backend test lint \
        docker-exporter docker-esxi-exporter docker-backend \
        docker-all compose-up compose-esxi compose-backend clean

# --- Binaries ---

all: build

build: build-exporter build-backend

build-exporter:
	mkdir -p bin
	docker run --rm --network host --tmpfs /root/.cache/go-build:exec \
		-v "$(shell pwd)":/src -w /src \
		golang:1.22-alpine sh -c \
		'CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /src/bin/inventory-exporter ./cmd/inventory-exporter/'

build-backend:
	mkdir -p bin
	docker run --rm --network host --tmpfs /root/.cache/go-build:exec \
		-v "$(shell pwd)":/src -w /src \
		golang:1.22-alpine sh -c \
		'go build -ldflags="-s -w" -o /src/bin/inventory-backend ./cmd/inventory-backend/'

# --- Docker images (require binaries built first) ---

docker-exporter: build-exporter
	docker build --network host -f docker/exporter.Dockerfile \
		-t inventory-exporter:latest .

docker-backend: build-backend
	docker build --network host -f docker/backend.Dockerfile \
		-t inventory-backend:latest .

docker-all: docker-exporter docker-backend

# --- Docker Compose ---

compose-up:
	docker-compose up -d

compose-esxi:
	docker-compose -f docker-compose.esxi-exporter.yml up -d

compose-backend:
	PROMETHEUS_URL=$(PROMETHEUS_URL) \
	CONFLUENCE_URL=$(CONFLUENCE_URL) \
	CONFLUENCE_USERNAME=$(CONFLUENCE_USERNAME) \
	CONFLUENCE_PASSWORD=$(CONFLUENCE_PASSWORD) \
	CONFLUENCE_SPACE_KEY=$(CONFLUENCE_SPACE_KEY) \
	docker-compose -f docker-compose.backend.yml up -d

# --- Test ---

test:
	docker run --rm --network host --tmpfs /root/.cache/go-build:exec \
		-v "$(shell pwd)":/src -w /src \
		golang:1.22-alpine sh -c 'go test ./... -count=1 -v'

test-race:
	docker run --rm --network host --tmpfs /root/.cache/go-build:exec \
		-v "$(shell pwd)":/src -w /src \
		golang:1.22-alpine sh -c 'go test ./... -race -count=1'

lint:
	docker run --rm --network host \
		-v "$(shell pwd)":/src -w /src \
		golang:1.22-alpine sh -c 'go vet ./...'

# --- Clean ---

clean:
	rm -rf bin/
