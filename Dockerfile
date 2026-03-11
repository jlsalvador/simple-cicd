# Build stage
# BUILDPLATFORM = the host running docker buildx (used for the compiler)
# TARGETPLATFORM = the platform being produced (TARGETOS / TARGETARCH)
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev

WORKDIR /app
COPY go.mod ./
# No external dependencies – no need to download modules
COPY cmd cmd
COPY internal internal

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-s -w -X github.com/jlsalvador/simple-cicd/internal/version.Version=${VERSION}" \
    -o simple-cicd ./cmd/simple-cicd

# Runtime stage – scratch works for all architectures
FROM scratch

COPY --from=builder /app/simple-cicd /simple-cicd

ENTRYPOINT ["/simple-cicd"]
