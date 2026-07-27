# syntax=docker/dockerfile:1

# ---- build stage ----
# Runs on the builder's native platform and cross-compiles to the target, so
# multi-arch images build fast (no QEMU emulation of the Go toolchain).
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src

# Module graph first for better layer caching. The project is currently
# dependency-free, so this is effectively a no-op, but it keeps the pattern.
COPY go.mod ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0

RUN GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
	-ldflags "-s -w \
	-X github.com/bumshi/bumshi/server/internal/version.Version=${VERSION} \
	-X github.com/bumshi/bumshi/server/internal/version.Commit=${COMMIT} \
	-X github.com/bumshi/bumshi/server/internal/version.BuildDate=${BUILD_DATE}" \
	-o /out/bumshid ./cmd/bumshid

# ---- runtime stage ----
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/bumshid /usr/local/bin/bumshid
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/bumshid"]
