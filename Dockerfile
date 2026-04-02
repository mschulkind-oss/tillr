# Build stage
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o tillr ./cmd/tillr

# Runtime stage — pure Go binary, no libc needed
FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/tillr /usr/local/bin/tillr
EXPOSE 3847
ENTRYPOINT ["tillr"]
CMD ["serve", "--port", "3847"]
