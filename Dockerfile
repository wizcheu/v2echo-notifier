FROM node:22-bookworm-slim AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /notifier /notifier
COPY --from=backend --chown=65532:65532 /data /data
USER 65532:65532
EXPOSE 8282
ENTRYPOINT ["/notifier"]
CMD ["-listen", "0.0.0.0:8282", "-data", "/data"]
