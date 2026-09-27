FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
ARG SERVICE
RUN --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -trimpath -o /service ./cmd/${SERVICE}
FROM alpine:3.23
RUN adduser -D demo
USER demo
COPY --from=build /service /service
EXPOSE 8080
ENTRYPOINT ["/service"]
