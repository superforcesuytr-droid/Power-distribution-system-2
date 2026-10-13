# The application served to other computers - on a company network or as a
# hosted demonstration - rather than shown in a window. It reads its database
# from DATABASE_URL and listens on PORT, as cloud hosts provide them.
#
#   docker build -t pds .
#   docker run -e DATABASE_URL=postgres://... -e PDS_ACCESS_PASSWORD=... -p 8080:8080 pds
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /pds ./cmd/pds

FROM alpine:3.20
RUN adduser -D -h /app app
USER app
WORKDIR /app
COPY --from=build /pds /app/pds
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/app/pds", "--config", "/app/config.json"]
