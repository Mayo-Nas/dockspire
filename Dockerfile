FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8 AS build
ENV GOTOOLCHAIN=local GOTELEMETRY=off
RUN test "$(go env GOVERSION)" = "go1.27.2"
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
COPY licenses ./licenses
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /stacklane ./cmd/dockspire

FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8 AS compose
ENV GOTOOLCHAIN=local GOTELEMETRY=off CGO_ENABLED=0
WORKDIR /compose
RUN test "$(go env GOVERSION)" = "go1.27.2"
COPY deploy/compose-runtime/go.mod deploy/compose-runtime/go.sum ./
RUN go mod download && go mod verify
RUN go build -mod=readonly -trimpath -tags=e2e -ldflags="-w -X github.com/docker/compose/v5/internal.Version=v5.6.0-dockspire.1" -o /docker-compose github.com/docker/compose/v5/cmd

FROM scratch
COPY --from=build /stacklane /stacklane
COPY --from=compose /docker-compose /docker-compose
COPY --from=build /src/licenses /licenses
LABEL org.opencontainers.image.source="https://github.com/Mayo-Nas/dockspire"
EXPOSE 9443
ENTRYPOINT ["/stacklane"]
CMD ["--https", "--setup", "--data-dir", "/appdata"]
