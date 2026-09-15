FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/graph-log-watcher ./cmd/graph-log-watcher

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/graph-log-watcher /graph-log-watcher
EXPOSE 9108
ENTRYPOINT ["/graph-log-watcher"]
