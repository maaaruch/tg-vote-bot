# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS builder

# sqlite3 использует CGO, поэтому на этапе сборки нужны gcc и musl
RUN apk add --no-cache gcc musl-dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# собираем бинарник из cmd/bot
RUN CGO_ENABLED=1 go build -o /out/bot ./cmd/bot


FROM alpine:3.20

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=builder /out/bot ./bot
COPY assets ./assets

RUN mkdir -p /app/data

ENV TELEGRAM_BOT_TOKEN=""
ENV DB_PATH="/app/data/data.db"
ENV VOTE_SALT="dev_salt_change_me"
ENV BOT_DEBUG="false"

# персистим только БД
VOLUME ["/app/data"]

CMD ["./bot"]