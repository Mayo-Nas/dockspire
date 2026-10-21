FROM golang:1.27.3@sha256:6928d94d1142af58816fed5a62338d605a266ccd53a0deaab6b1096f19059261 AS build
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
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/Mayo-Nas/dockspire/internal/app.Version=${VERSION}" -o /stacklane ./cmd/dockspire

FROM golang:1.27.3@sha256:6928d94d1142af58816fed5a62338d605a266ccd53a0deaab6b1096f19059261 AS compose
ENV GOTOOLCHAIN=local GOTELEMETRY=off CGO_ENABLED=0
WORKDIR /compose
RUN test "$(go env GOVERSION)" = "go1.27.3"
COPY deploy/compose-runtime/go.mod deploy/compose-runtime/go.sum ./
RUN go mod download && go mod verify
RUN go build -mod=readonly -trimpath -tags=e2e -ldflags="-w -X github.com/docker/compose/v5/internal.Version=v5.6.0-dockspire.1" -o /docker-compose github.com/docker/compose/v5/cmd

FROM scratch
COPY --from=build /stacklane /stacklane
COPY --from=compose /docker-compose /docker-compose
COPY --from=build /src/licenses /licenses
LABEL org.opencontainers.image.source="https://github.com/Mayo-Nas/dockspire"
EXPOSE 8787
ENTRYPOINT ["/stacklane"]
CMD ["--allow-http", "--setup", "--data-dir", "/appdata"]
