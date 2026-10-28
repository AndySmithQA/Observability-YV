FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod .
RUN go mod download
COPY main.go .
RUN go mod tidy
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /worker .

FROM alpine:3.20
COPY --from=build /worker /worker
EXPOSE 9101
CMD ["/worker"]
