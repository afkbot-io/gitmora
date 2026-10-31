# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
ARG NODE_IMAGE=node:24.22.2-alpine3.24@sha256:8a32c8cf649a49915a66e81e7a609223c708d0adabf288ee3f4077d765542f85
ARG GO_IMAGE=golang:1.27.3-alpine3.24@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb

FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS web
WORKDIR /source
COPY package.json package-lock.json ./
# Native agent test postinstall is unnecessary for the frontend; all shipped assets come from the lockfile build.
RUN --mount=type=cache,target=/root/.npm npm ci --ignore-scripts --no-audit --no-fund
COPY web/ web/
COPY scripts/build-web.mjs scripts/build-web.mjs
COPY internal/transport/webui/assets/README.txt internal/transport/webui/assets/README.txt
RUN npm run build:web

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS go
ARG TARGETOS
ARG TARGETARCH
ARG RELEASE_VERSION=development
ARG RELEASE_REVISION=
ENV GOTOOLCHAIN=local CGO_ENABLED=0
WORKDIR /source
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY internal/ internal/
COPY cmd/ cmd/
COPY --from=web /source/internal/transport/webui/assets/ internal/transport/webui/assets/
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    GOOS=$TARGETOS GOARCH=$TARGETARCH GOAMD64=v1 GOARM64=v8.0 go build -trimpath -buildvcs=false \
    -ldflags "-buildid= -X workgraph.local/platform/internal/distribution/buildinfo.version=$RELEASE_VERSION -X workgraph.local/platform/internal/distribution/buildinfo.revision=$RELEASE_REVISION" \
    -o /out/gitmorad ./cmd/workgraphd

FROM scratch AS runtime
ARG RELEASE_VERSION=development
ARG RELEASE_REVISION=
LABEL org.opencontainers.image.title="Gitmora" org.opencontainers.image.description="Private engineering evidence API and workers"
LABEL org.opencontainers.image.version="$RELEASE_VERSION" org.opencontainers.image.revision="$RELEASE_REVISION"
COPY --from=go /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=go /out/gitmorad /gitmorad
USER 65532:65532
WORKDIR /
EXPOSE 8080
ENTRYPOINT ["/gitmorad"]
CMD ["serve", "--listen", "0.0.0.0:8080"]
