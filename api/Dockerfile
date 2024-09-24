FROM golang:alpine AS build

WORKDIR /app

# Copy the Go module files
COPY go.mod .
COPY go.sum .

# Download the Go module dependencies
RUN go mod download

COPY . .

RUN GOOS=linux go build -o server

FROM alpine:3.20.1 
WORKDIR /app
RUN apk --no-cache add curl
RUN apk add ca-certificates
COPY --from=build /app/server server
CMD ["/app/server"]
