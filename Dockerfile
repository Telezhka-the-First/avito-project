FROM golang:1.23-alpine AS build

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

RUN go build -o review-assigner-service ./cmd/server

FROM alpine:3.21

WORKDIR /app

COPY --from=build /app/review-assigner-service .

EXPOSE 8080

CMD ["./review-assigner-service"]
