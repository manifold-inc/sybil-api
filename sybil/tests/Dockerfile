FROM golang:1.21-alpine

WORKDIR /app

# Copy the test file
COPY test_endon.go .

# Build and run the test
CMD ["go", "run", "test_endon.go"]
