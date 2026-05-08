# syntax=docker/dockerfile:1.7

FROM golang:1.24.9-bookworm AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY . .

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/uptime-service ./

FROM gcr.io/distroless/cc-debian12:nonroot

WORKDIR /app
COPY --from=builder /out/uptime-service /app/uptime-service

USER nonroot:nonroot

ENTRYPOINT ["/app/uptime-service"]
