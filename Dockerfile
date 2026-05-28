FROM golang:latest AS builder

WORKDIR /app

COPY go.mod go.sum main.go ./
RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux go build -o /unidex

FROM scratch

COPY --from=builder /unidex /unidex
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

EXPOSE 8080

CMD ["/unidex"]