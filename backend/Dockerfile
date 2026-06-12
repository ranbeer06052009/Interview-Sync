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
# we need to install the compilers in the Alpine container.
RUN apk add --no-cache python3 nodejs npm g++ openjdk17 rust
RUN npm install -g typescript ts-node

EXPOSE 8080
ENTRYPOINT ["/server"]
