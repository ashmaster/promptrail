ARG GO_VERSION=1
FROM golang:${GO_VERSION}-bookworm AS builder

WORKDIR /usr/src/app
COPY go.mod go.sum ./
RUN go mod download && go mod verify
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -v -o /run-app ./server

FROM debian:bookworm

COPY --from=builder /run-app /usr/local/bin/
EXPOSE 8080
CMD ["run-app"]