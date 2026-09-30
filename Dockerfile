# Builder: the Go version must match the `go` line in go.mod.
FROM golang:1.25.4-trixie@sha256:a02d35efc036053fdf0da8c15919276bf777a80cbfda6a35c5e9f087e652adfc AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The context has no .git, so the version comes in as a build arg (make image).
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=${VERSION}" -o /out/gateway ./cmd/gateway
# The capture store's mount point. distroless has no shell to mkdir with, and a named
# volume mounted where the image has no directory is created owned by root, which
# nonroot can't write. Docker initialises an empty volume with this directory's owner.
RUN mkdir /out/capture

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=build /out/gateway /gateway
COPY --from=build --chown=nonroot /out/capture /var/lib/llm-gateway
USER nonroot
ENTRYPOINT ["/gateway"]
