FROM golang:alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /server

FROM alpine:latest
WORKDIR /app
COPY --from=builder /server /server

# Note: For Option B local execution to work in Docker, 
# you'll need compilers installed here (e.g., RUN apk add python3 nodejs g++ openjdk11).
# We are including Python and Node.js for standard execution support.
RUN apk add --no-cache python3 nodejs

EXPOSE 8080
ENTRYPOINT ["/server"]
