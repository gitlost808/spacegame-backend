FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 go test ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.24

RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app

WORKDIR /app
COPY --from=build /out/server /out/migrate /usr/local/bin/
COPY migrations ./migrations

ENV HTTP_ADDR=:8080 GIN_MODE=release
COPY internal/namefilter/words/LICENSE internal/namefilter/words/NOTICE /usr/share/licenses/spacegame-backend/namefilter/
USER 10001:10001
EXPOSE 8080
STOPSIGNAL SIGTERM
CMD ["server"]
