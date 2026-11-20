FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
ENV GOTOOLCHAIN=local GOTELEMETRY=off
RUN test "$(go env GOVERSION)" = "go1.27.3"
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
COPY licenses ./licenses
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/Mayo-Nas/dockspire/internal/app.Version=${VERSION}" -o /dockspire ./cmd/dockspire

FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS compose
ENV GOTOOLCHAIN=local GOTELEMETRY=off CGO_ENABLED=0
WORKDIR /compose
RUN test "$(go env GOVERSION)" = "go1.27.3"
COPY deploy/compose-runtime/go.mod deploy/compose-runtime/go.sum ./
RUN go mod download && go mod verify
RUN go build -mod=readonly -trimpath -tags=e2e -ldflags="-w -X github.com/docker/compose/v5/internal.Version=v5.6.0-dockspire.1" -o /docker-compose github.com/docker/compose/v5/cmd

FROM scratch
COPY --from=build /dockspire /dockspire
COPY --from=compose /docker-compose /docker-compose
COPY --from=build /src/licenses /licenses
COPY LICENSE NOTICE /licenses/
LABEL org.opencontainers.image.source="https://github.com/Mayo-Nas/dockspire"
EXPOSE 8787
ENTRYPOINT ["/dockspire"]
CMD ["--allow-http", "--setup", "--data-dir", "/appdata"]
