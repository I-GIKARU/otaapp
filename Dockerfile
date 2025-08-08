# Build stage
FROM golang:1.23 AS builder

WORKDIR /app

# First copy only the files needed for dependencies
COPY go.mod go.sum ./

# Download dependencies (with retry)
RUN go mod download || go mod download

# Copy the rest of the code and credentials
COPY . .
COPY adrian-plus-project-firebase-adminsdk-fbsvc-81e60c3ca4.json .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -o ota-server

# Final stage
FROM gcr.io/distroless/static-debian11
WORKDIR /app
COPY --from=builder /app/ota-server .
COPY --from=builder /app/adrian-plus-project-firebase-adminsdk-fbsvc-81e60c3ca4.json .
EXPOSE 8080
CMD ["./ota-server"]