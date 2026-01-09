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

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
    -o /kubelogs-collector \
    ./cmd/collector

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /kubelogs-collector /kubelogs-collector

USER nonroot:nonroot

ENTRYPOINT ["/kubelogs-collector"]
