FROM golang:alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o server .

FROM alpine:3.19
COPY --from=builder /app/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
