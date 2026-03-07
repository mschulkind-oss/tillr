# Build stage
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o lifecycle ./cmd/lifecycle

# Runtime stage — pure Go binary, no libc needed
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/lifecycle /usr/local/bin/lifecycle
EXPOSE 3847
ENTRYPOINT ["lifecycle"]
CMD ["serve", "--port", "3847"]
