################################################################################
# Stage 1 — build a fully static binary using the official Go toolchain.
################################################################################
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-alpine AS builder

# Build deps for CGO-free static linking.
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Cache go.mod / go.sum first so dependency layers stay warm across edits.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source tree.
COPY . .

# CGO disabled so the binary is fully static and runs on scratch / distroless.
# TARGETOS / TARGETARCH are auto-injected by BuildKit when building with
# `docker buildx --platform=...`, enabling multi-arch builds.
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH}
RUN go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

################################################################################
# Stage 2 — minimal runtime image.
################################################################################
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=builder /out/server /app/server
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/app/server"]
