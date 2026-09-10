FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/regulator ./cmd/regulator

FROM alpine:3.21
RUN addgroup -S regulator && adduser -S -G regulator regulator
COPY --from=build /out/regulator /usr/local/bin/regulator
USER regulator
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/regulator"]

