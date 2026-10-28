FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/icloud-mail ./cmd/icloud-mail

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -H -u 65532 app
USER 65532:65532
COPY --from=build /out/icloud-mail /usr/local/bin/icloud-mail
EXPOSE 8080
ENTRYPOINT ["icloud-mail"]
