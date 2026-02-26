package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/mattn/go-sqlite3"

	"tg-vote-bot/internal/app"
	"tg-vote-bot/internal/storage"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN не задан")
	}

	dbPath := getenv("DB_PATH", "data/data.db")
	voteSalt := getenv("VOTE_SALT", "dev_salt_change_me")
	debug := getbool("BOT_DEBUG", false)

	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		log.Fatalf("ошибка открытия БД: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := storage.New(db)
	if err := store.InitSchema(); err != nil {
		log.Fatalf("ошибка инициализации схемы БД: %v", err)
	}

	bot, err := tgbotapi.NewBotAPI(token)
	if err != nil {
		log.Fatalf("ошибка создания бота: %v", err)
	}
	bot.Debug = debug
	log.Printf("Бот запущен как @%s", bot.Self.UserName)

	application := app.New(bot, store, voteSalt)
	application.Run(ctx)

	log.Println("Выключаемся…")
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getbool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
