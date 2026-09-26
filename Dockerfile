# syntax=docker/dockerfile:1.7
FROM golang:1.23-alpine AS deps
RUN apk add --no-cache ca-certificates
WORKDIR /src
COPY pkg/go.mod pkg/go.sum ./pkg/
# erp-schema's `replace` (go.mod, only in the services that import it) points at this local
# module path, not a proxy — `go mod download` needs the real source here, not just go.mod/go.sum.
COPY schema/generated/go ./schema/generated/go
ARG SERVICE
COPY services/${SERVICE}/go.mod services/${SERVICE}/go.sum ./services/${SERVICE}/
WORKDIR /src/services/${SERVICE}
ENV GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

FROM deps AS src
ARG SERVICE
WORKDIR /src
COPY pkg ./pkg
COPY services/${SERVICE} ./services/${SERVICE}

FROM src AS test
ARG SERVICE
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go test -C /src/pkg ./... && \
    CGO_ENABLED=0 go test -C /src/services/${SERVICE} ./...

FROM src AS build
ARG SERVICE
ARG TARGETARCH
WORKDIR /src/services/${SERVICE}
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM test AS verified
COPY --from=build /out/api /out/api

FROM scratch
COPY --from=deps /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=busybox:1.37.0-uclibc /bin/busybox /bin/busybox
COPY --from=verified /out/api /api
USER 65532:65532
EXPOSE 8080
CMD ["/api"]
