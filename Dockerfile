FROM node:22-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
ARG APP_VERSION
ARG APP_REVISION
RUN VITE_APP_VERSION="$APP_VERSION" VITE_APP_REVISION="$APP_REVISION" npm run build

FROM golang:1.26-bookworm AS backend
WORKDIR /src
COPY go.mod go.sum ./
ARG GOPROXY=https://proxy.golang.org,direct
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -o /notifier ./cmd/notifier
RUN mkdir -p /data && chmod 0700 /data

# The Go binary is static. Reuse runtime data from the builder so the final
# image does not need a separate registry (such as gcr.io) to be reachable.
FROM scratch
COPY --from=backend /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=backend /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=backend /notifier /notifier
COPY --from=backend --chown=65532:65532 /data /data
USER 65532:65532
EXPOSE 8282
ENTRYPOINT ["/notifier"]
CMD ["-listen", "0.0.0.0:8282", "-data", "/data"]
