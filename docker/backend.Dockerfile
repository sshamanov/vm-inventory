# Run `make build-backend` first to produce bin/inventory-backend.
# Then: docker build --network host -f docker/backend.Dockerfile -t inventory-backend .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY bin/inventory-backend /usr/bin/inventory-backend
COPY web/ /usr/share/inventory-backend/web/

ENV WEB_DIR=/usr/share/inventory-backend/web/
ENV LISTEN_ADDR=:8080

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:8080/api/status || exit 1

ENTRYPOINT ["/usr/bin/inventory-backend"]
