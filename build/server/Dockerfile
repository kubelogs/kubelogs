# Build stage
FROM golang:1.25.5-bookworm AS builder

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=1 GOOS=linux go build \
    -tags "fts5" \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
    -o /kubelogs-server \
    ./cmd/server

# Runtime stage
FROM gcr.io/distroless/base-debian12:nonroot

COPY --from=builder /kubelogs-server /kubelogs-server

# gRPC and HTTP ports
EXPOSE 50051 8080

USER nonroot:nonroot

ENTRYPOINT ["/kubelogs-server"]
