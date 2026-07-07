FROM golang:1.22-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/custody-api ./cmd/custody-api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/custody-api /custody-api
EXPOSE 8080

ENTRYPOINT ["/custody-api"]
