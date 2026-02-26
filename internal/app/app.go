package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/maaaruch/tg-vote-bot/internal/session"
	"github.com/maaaruch/tg-vote-bot/internal/storage"
)

type App struct {
	bot      *tgbotapi.BotAPI
	store    *storage.Store
	sessions *session.Manager
	voteSalt string
}

func New(bot *tgbotapi.BotAPI, store *storage.Store, voteSalt string) *App {
	return &App{
		bot:      bot,
		store:    store,
		sessions: session.NewManager(),
		voteSalt: voteSalt,
	}
}

func (a *App) Run(ctx context.Context) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := a.bot.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			a.bot.StopReceivingUpdates()
			return

		case update, ok := <-updates:
			if !ok {
				return
			}
			if update.Message != nil {
				a.handleMessage(update.Message)
			} else if update.CallbackQuery != nil {
				a.handleCallback(update.CallbackQuery)
			}
		}
	}
}

func (a *App) getSession(userID int64) *session.Session {
	return a.sessions.Get(userID)
}

func (a *App) hashUserID(userID int64) string {
	data := fmt.Sprintf("%s:%d", a.voteSalt, userID)
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// ---------- Updates ----------

func (a *App) handleMessage(msg *tgbotapi.Message) {
	if msg.From == nil {
		return
	}
	userID := msg.From.ID
	sess := a.getSession(userID)

	// 1) ждём медиа для номинанта
	if sess.WaitingMediaForNomineeID != 0 && (len(msg.Photo) > 0 || msg.Video != nil) {
		a.handleMediaUpload(msg, sess)
		return
	}

	// 2) ждём имя нового номинанта (после кнопки "➕ Добавить номинанта")
	if sess.CreatingNomineeForNominationID != 0 && !msg.IsCommand() && strings.TrimSpace(msg.Text) != "" {
		a.handleCreateNomineeTextStep(msg, sess)
		return
	}

	// 3) команды
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			text := "Привет! Это бот для голосования по номинациям в комнатах.\n\n" +
				"Основные команды:\n" +
				"/create_room Название | Пароль – создать свою комнату\n" +
				"/my_rooms – список твоих комнат\n" +
				"/room ID Пароль – войти в комнату как участник\n" +
				"/nominations – показать номинации в активной комнате (с ID)\n" +
				"/add_nomination roomID | Название | Описание – добавить номинацию (только автор комнаты)\n" +
				"/add_nominee nominationID | Имя – добавить номинанта\n" +
				"/set_nominee_media nomineeID – привязать/сменить фото/видео номинанта\n" +
				"/delete_nomination nominationID – удалить номинацию\n" +
				"/delete_nominee nomineeID – удалить номинанта\n" +
				"/results nominationID – результаты одной номинации (только автор комнаты)"
			photo := tgbotapi.NewPhoto(msg.Chat.ID, tgbotapi.FilePath("assets/start.jpg"))
			photo.Caption = text
			a.bot.Send(photo)

		case "help":
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Смотри /start – там всё расписано 🙂"))

		case "create_room":
			a.handleCreateRoom(msg)

		case "my_rooms":
			a.handleMyRooms(msg)

		case "room":
			a.handleJoinRoom(msg)

		case "nominations":
			a.handleNominationsCommand(msg, sess)

		case "add_nomination":
			a.handleAddNomination(msg)

		case "add_nominee":
			a.handleAddNominee(msg)

		case "set_nominee_media":
			a.handleSetNomineeMedia(msg, sess)

		case "delete_nomination":
			a.handleDeleteNomination(msg)

		case "delete_nominee":
			a.handleDeleteNominee(msg)

		case "results":
			a.handleResults(msg)

		default:
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не знаю такой команды. Попробуй /start"))
		}
		return
	}

	// 4) просто текст
	if strings.Contains(strings.ToLower(msg.Text), "номинац") {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Чтобы увидеть номинации в комнате – используй команду /nominations (после /room)."))
	}
}

func (a *App) handleCallback(cq *tgbotapi.CallbackQuery) {
	data := cq.Data
	if cq.From == nil {
		return
	}
	userID := cq.From.ID
	sess := a.getSession(userID)

	// убрать "часики" у кнопки
	_, _ = a.bot.Request(tgbotapi.NewCallback(cq.ID, ""))

	// универсальная кнопка "назад" — возвращаемся к списку номинаций
	if data == "back:nominations" {
		// сбрасываем возможные "ожидания" (имя/медиа), чтобы пользователь не застревал в режиме ввода
		sess.WaitingMediaForNomineeID = 0
		sess.CreatingNomineeForNominationID = 0

		if sess.ActiveRoomID == 0 {
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Сначала зайди в комнату: /room ID Пароль"))
			return
		}
		if err := a.sendNominationsList(cq.Message.Chat.ID, userID, sess.ActiveRoomID); err != nil {
			log.Println("back:nominations -> sendNominationsList:", err)
		}
		return
	}

	switch {
	// открыть номинацию, показать номинантов
	case strings.HasPrefix(data, "nomination:"):
		idStr := strings.TrimPrefix(data, "nomination:")
		nomID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return
		}

		roomID, err := a.store.GetNominationRoomID(nomID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Эта номинация больше не существует."))
			} else {
				log.Println("get nomination room:", err)
			}
			return
		}

		if sess.ActiveRoomID != roomID {
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "У тебя нет доступа к этой комнате. Сначала зайди в неё командой /room."))
			return
		}

		if err := a.sendNominees(cq.Message.Chat.ID, cq.From.ID, nomID); err != nil {
			log.Println("sendNominees:", err)
		}

	// голосование за номинанта
	case strings.HasPrefix(data, "vote:"):
		idStr := strings.TrimPrefix(data, "vote:")
		nomineeID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return
		}

		nominationID, roomID, err := a.store.GetNomineeNominationAndRoom(nomineeID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Этот номинант больше не существует."))
			} else {
				log.Println("get nominee nomination/room:", err)
			}
			return
		}

		if sess.ActiveRoomID != roomID {
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "У тебя нет доступа к этой комнате. Сначала зайди в неё командой /room."))
			return
		}

		userHash := a.hashUserID(userID)
		if err := a.store.RecordVote(userHash, nominationID, nomineeID, time.Now()); err != nil {
			log.Println("record vote:", err)
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Что-то пошло не так, попробуй ещё раз."))
			return
		}

		name, err := a.store.GetNomineeName(nomineeID)
		if err != nil {
			log.Println("get nominee name:", err)
		}
		if name == "" {
			name = "выбранного номинанта"
		}
		text := fmt.Sprintf("Голос принят! Ты проголосовал за: %s", name)
		a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, text))

		// сразу снова показываем список номинаций, чтобы не нужно было листать вверх
		if sess.ActiveRoomID != 0 {
			if err := a.sendNominationsList(cq.Message.Chat.ID, userID, sess.ActiveRoomID); err != nil {
				log.Println("sendNominationsList(after vote):", err)
			}
		}

	// результаты по номинации (кнопка 📊 Результаты)
	case strings.HasPrefix(data, "res_nom:"):
		idStr := strings.TrimPrefix(data, "res_nom:")
		nominationID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return
		}

		roomID, err := a.store.GetNominationRoomID(nominationID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Номинация не найдена."))
			} else {
				log.Println("res_nom get room:", err)
				a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Ошибка при получении номинации."))
			}
			return
		}

		isOwner, err := a.store.IsRoomOwner(roomID, cq.From.ID)
		if err != nil {
			log.Println("IsRoomOwner(res_nom):", err)
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Ошибка проверки прав."))
			return
		}
		if !isOwner {
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Результаты может смотреть только автор комнаты."))
			return
		}

		roomTitle, err := a.store.GetRoomTitle(roomID)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			log.Println("res_nom get room title:", err)
		}
		if roomTitle == "" {
			roomTitle = fmt.Sprintf("ID %d", roomID)
		}

		nominationName, err := a.store.GetNominationName(nominationID)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			log.Println("res_nom get nomination name:", err)
		}
		if nominationName == "" {
			nominationName = fmt.Sprintf("ID %d", nominationID)
		}

		results, err := a.store.ResultsByNomination(nominationID)
		if err != nil {
			log.Println("res_nom results:", err)
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Не удалось получить результаты."))
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf(
			"Результаты голосования\nКомната: %s (ID %d)\nНоминация: %s (ID %d)\n\n",
			roomTitle, roomID, nominationName, nominationID,
		))

		if len(results) == 0 {
			sb.WriteString("В этой номинации пока нет номинантов.\n")
		} else {
			for _, r := range results {
				sb.WriteString(fmt.Sprintf("• %s (ID %d) — %d голос(ов)\n", r.Name, r.ID, r.Votes))
			}
		}

		text := sb.String()
		if len(text) > 4000 {
			text = text[:4000] + "\n\n(обрезано, слишком много текста)"
		}
		m := tgbotapi.NewMessage(cq.Message.Chat.ID, text)
		m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад к номинациям", "back:nominations"),
			),
		)
		a.bot.Send(m)

	// кнопка "➕ Добавить номинанта"
	case strings.HasPrefix(data, "addnom:"):
		idStr := strings.TrimPrefix(data, "addnom:")
		nominationID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return
		}

		ok, err := a.store.IsNominationOwner(nominationID, cq.From.ID)
		if err != nil {
			log.Println("IsNominationOwner(addnom):", err)
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Ошибка проверки прав."))
			return
		}
		if !ok {
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Только автор комнаты может добавлять номинантов."))
			return
		}

		sess.CreatingNomineeForNominationID = nominationID
		sess.WaitingMediaForNomineeID = 0

		m := tgbotapi.NewMessage(cq.Message.Chat.ID, "Отправь имя нового номинанта одним текстовым сообщением.")
		m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад к номинациям", "back:nominations"),
			),
		)
		a.bot.Send(m)

	// кнопка "🖼 Медиа" у номинанта
	case strings.HasPrefix(data, "setmedia:"):
		idStr := strings.TrimPrefix(data, "setmedia:")
		nomineeID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return
		}

		ok, err := a.store.IsNomineeOwner(nomineeID, cq.From.ID)
		if err != nil {
			log.Println("IsNomineeOwner(setmedia):", err)
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Ошибка проверки прав."))
			return
		}
		if !ok {
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Только автор комнаты может менять медиа у номинантов."))
			return
		}

		sess.WaitingMediaForNomineeID = nomineeID
		sess.CreatingNomineeForNominationID = 0

		m := tgbotapi.NewMessage(cq.Message.Chat.ID, "Ок! Теперь отправь фото или видео для этого номинанта одним следующим сообщением.")
		m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад к номинациям", "back:nominations"),
			),
		)
		a.bot.Send(m)

	// кнопка "🗑 Удалить" у номинанта
	case strings.HasPrefix(data, "delnom:"):
		idStr := strings.TrimPrefix(data, "delnom:")
		nomineeID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return
		}

		ok, err := a.store.IsNomineeOwner(nomineeID, cq.From.ID)
		if err != nil {
			log.Println("IsNomineeOwner(delnom):", err)
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Ошибка проверки прав."))
			return
		}
		if !ok {
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Только автор комнаты может удалять номинантов."))
			return
		}

		deleted, err := a.store.DeleteNominee(nomineeID)
		if err != nil {
			log.Println("DeleteNominee(delnom):", err)
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Не удалось удалить номинанта."))
			return
		}
		if !deleted {
			a.bot.Send(tgbotapi.NewMessage(cq.Message.Chat.ID, "Номинант с таким ID не найден."))
			return
		}

		m := tgbotapi.NewMessage(cq.Message.Chat.ID, "Номинант удалён вместе с его голосами ✅\n"+
			"Если хочешь обновить список, просто снова открой эту номинацию.")
		m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад к номинациям", "back:nominations"),
			),
		)
		a.bot.Send(m)
	}
}

// ---------- Команды ----------

func (a *App) handleCreateRoom(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		text := "Формат: /create_room Название | Пароль\n\nПример:\n/create_room Новый год 2025 | secret123"
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
		return
	}

	parts := splitPipeArgs(args, 2)
	if len(parts) < 2 {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Нужно указать и название, и пароль через '|'"))
		return
	}

	title := parts[0]
	password := parts[1]

	roomID, err := a.store.CreateRoom(msg.From.ID, title, password)
	if err != nil {
		log.Println("create_room:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не удалось создать комнату 😔"))
		return
	}

	text := fmt.Sprintf(
		"Комната создана! 🎉\nID: %d\nНазвание: %s\nПароль: %s\n\n"+
			"Поделись ID и паролем с участниками.\n"+
			"Чтобы зайти как участник: /room %d %s",
		roomID, title, password, roomID, password)
	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
}

func (a *App) handleMyRooms(msg *tgbotapi.Message) {
	rooms, err := a.store.ListRoomsByOwner(msg.From.ID)
	if err != nil {
		log.Println("my_rooms:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не получилось получить список комнат."))
		return
	}

	if len(rooms) == 0 {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "У тебя пока нет комнат. Создай: /create_room Название | Пароль"))
		return
	}

	var sb strings.Builder
	sb.WriteString("Твои комнаты:\n")
	for _, r := range rooms {
		sb.WriteString(fmt.Sprintf("• ID: %d — %s\n", r.ID, r.Title))
	}
	sb.WriteString("\nЧтобы зайти в комнату как участник:\n/room ID Пароль")

	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, sb.String()))
}

func (a *App) handleJoinRoom(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Формат: /room ID Пароль\nПример: /room 1 secret123"))
		return
	}

	fields := strings.Fields(args)
	if len(fields) < 2 {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Нужно указать ID и пароль."))
		return
	}

	roomID, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "ID комнаты должно быть числом."))
		return
	}

	password := fields[1]

	room, err := a.store.GetRoomByIDAndPassword(roomID, password)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Комната не найдена или неверный пароль."))
		} else {
			log.Println("join room:", err)
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка при входе в комнату."))
		}
		return
	}

	sess := a.getSession(msg.From.ID)
	sess.ActiveRoomID = room.ID

	text := fmt.Sprintf("Ты вошёл в комнату: %s (ID %d)\nТеперь можешь смотреть номинации командой /nominations", room.Title, room.ID)
	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
}

func (a *App) handleNominationsCommand(msg *tgbotapi.Message, sess *session.Session) {
	if sess.ActiveRoomID == 0 {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Сначала зайди в комнату: /room ID Пароль"))
		return
	}
	if err := a.sendNominationsList(msg.Chat.ID, msg.From.ID, sess.ActiveRoomID); err != nil {
		log.Println("nominations:", err)
	}
}

func (a *App) handleAddNomination(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		text := "Формат: /add_nomination roomID | Название | Описание(опц)\n\n" +
			"Пример:\n/add_nomination 1 | Лучший разработчик | За топовый код"
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
		return
	}

	parts := splitPipeArgs(args, 3)
	if len(parts) < 2 {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Нужно минимум roomID и название, разделённые '|'"))
		return
	}

	roomID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "roomID должно быть числом."))
		return
	}

	title := parts[1]
	description := ""
	if len(parts) >= 3 {
		description = parts[2]
	}

	ok, err := a.store.IsRoomOwner(roomID, msg.From.ID)
	if err != nil {
		log.Println("isRoomOwner:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки прав."))
		return
	}
	if !ok {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Только автор комнаты может добавлять номинации."))
		return
	}

	if _, err := a.store.CreateNomination(roomID, title, description); err != nil {
		log.Println("add_nomination:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не получилось добавить номинацию."))
		return
	}

	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Номинация добавлена ✅\nID можно посмотреть через /nominations."))
}

func (a *App) handleAddNominee(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		text := "Формат: /add_nominee nominationID | Имя\nПример:\n/add_nominee 1 | Иван Иванов"
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
		return
	}

	parts := splitPipeArgs(args, 2)
	if len(parts) < 2 {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Нужно указать nominationID и имя, разделённые '|'"))
		return
	}

	nominationID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "nominationID должно быть числом."))
		return
	}

	name := parts[1]

	ok, err := a.store.IsNominationOwner(nominationID, msg.From.ID)
	if err != nil {
		log.Println("isNominationOwner:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки прав."))
		return
	}
	if !ok {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Только автор комнаты может добавлять номинантов."))
		return
	}

	if _, err := a.store.CreateNominee(nominationID, name); err != nil {
		log.Println("add_nominee:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не получилось добавить номинанта."))
		return
	}

	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Номинант добавлен ✅\n"+
		"Чтобы добавить или сменить медиа, используй команду /set_nominee_media с ID этого номинанта."))
}

func (a *App) handleSetNomineeMedia(msg *tgbotapi.Message, sess *session.Session) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		text := "Формат: /set_nominee_media nomineeID\n\n" +
			"После команды отправь одним сообщением фото или видео для этого номинанта.\n" +
			"Команду можно вызывать повторно — медиа перезапишется."
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
		return
	}

	nomineeID, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "nomineeID должно быть числом."))
		return
	}

	ok, err := a.store.IsNomineeOwner(nomineeID, msg.From.ID)
	if err != nil {
		log.Println("isNomineeOwner:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки прав."))
		return
	}
	if !ok {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Только автор комнаты может менять медиа у номинантов."))
		return
	}

	sess.WaitingMediaForNomineeID = nomineeID
	sess.CreatingNomineeForNominationID = 0

	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ок! Теперь отправь фото или видео для этого номинанта одним следующим сообщением."))
}

func (a *App) handleDeleteNomination(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		text := "Формат: /delete_nomination nominationID\n\n" +
			"ID номинации можно посмотреть через /nominations (они указаны в списке)."
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
		return
	}

	nominationID, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "nominationID должно быть числом."))
		return
	}

	ok, err := a.store.IsNominationOwner(nominationID, msg.From.ID)
	if err != nil {
		log.Println("isNominationOwner(delete_nomination):", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки прав."))
		return
	}
	if !ok {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Только автор комнаты может удалять номинации."))
		return
	}

	deleted, err := a.store.DeleteNomination(nominationID)
	if err != nil {
		log.Println("delete nomination:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не удалось удалить номинацию."))
		return
	}
	if !deleted {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Номинация с таким ID не найдена."))
		return
	}

	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Номинация удалена вместе с её номинантами и голосами ✅"))
}

func (a *App) handleDeleteNominee(msg *tgbotapi.Message) {
	args := strings.TrimSpace(msg.CommandArguments())
	if args == "" {
		text := "Формат: /delete_nominee nomineeID\n\n" +
			"ID номинанта можно увидеть, когда смотришь номинацию — он выводится в подписи к карточке."
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
		return
	}

	nomineeID, err := strconv.ParseInt(args, 10, 64)
	if err != nil {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "nomineeID должно быть числом."))
		return
	}

	ok, err := a.store.IsNomineeOwner(nomineeID, msg.From.ID)
	if err != nil {
		log.Println("isNomineeOwner(delete_nominee):", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки прав."))
		return
	}
	if !ok {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Только автор комнаты может удалять номинантов."))
		return
	}

	deleted, err := a.store.DeleteNominee(nomineeID)
	if err != nil {
		log.Println("delete nominee:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не удалось удалить номинанта."))
		return
	}
	if !deleted {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Номинант с таким ID не найден."))
		return
	}

	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Номинант удалён вместе с его голосами ✅"))
}

func (a *App) handleResults(msg *tgbotapi.Message) {
	args := strings.Fields(strings.TrimSpace(msg.CommandArguments()))
	if len(args) == 0 {
		text := "Форматы:\n" +
			"/results nominationID – результаты одной номинации\n" +
			"/results roomID nominationID – то же самое, но с явным указанием комнаты\n\n" +
			"ID номинации можно посмотреть через /nominations."
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
		return
	}

	var roomID, nominationID int64
	var err error

	if len(args) == 1 {
		nominationID, err = strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "nominationID должно быть числом."))
			return
		}
		roomID, err = a.store.GetNominationRoomID(nominationID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Номинация не найдена."))
			} else {
				log.Println("results get room:", err)
				a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка при получении номинации."))
			}
			return
		}
	} else {
		roomID, err = strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "roomID должно быть числом."))
			return
		}
		nominationID, err = strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "nominationID должно быть числом."))
			return
		}
		ok, err := a.store.CheckNominationInRoom(nominationID, roomID)
		if err != nil {
			log.Println("results check nom in room:", err)
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка при проверке номинации."))
			return
		}
		if !ok {
			a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Номинация с таким ID не принадлежит этой комнате."))
			return
		}
	}

	ok, err := a.store.IsRoomOwner(roomID, msg.From.ID)
	if err != nil {
		log.Println("isRoomOwner(results):", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки прав."))
		return
	}
	if !ok {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Только автор комнаты может смотреть результаты."))
		return
	}

	roomTitle, err := a.store.GetRoomTitle(roomID)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			log.Println("results get room title:", err)
		}
		roomTitle = fmt.Sprintf("ID %d", roomID)
	}

	nominationName, err := a.store.GetNominationName(nominationID)
	if err != nil {
		if !errors.Is(err, storage.ErrNotFound) {
			log.Println("results get nomination name:", err)
		}
		nominationName = fmt.Sprintf("ID %d", nominationID)
	}

	results, err := a.store.ResultsByNomination(nominationID)
	if err != nil {
		log.Println("results nominees:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не удалось получить результаты."))
		return
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Результаты голосования\nКомната: %s (ID %d)\nНоминация: %s (ID %d)\n\n",
		roomTitle, roomID, nominationName, nominationID,
	))

	if len(results) == 0 {
		sb.WriteString("В этой номинации пока нет номинантов.\n")
	} else {
		for _, r := range results {
			sb.WriteString(fmt.Sprintf("• %s (ID %d) — %d голос(ов)\n", r.Name, r.ID, r.Votes))
		}
	}

	text := sb.String()
	if len(text) > 4000 {
		text = text[:4000] + "\n\n(обрезано, слишком много текста)"
	}
	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
}

// ---------- Медиа / создание номинантов / утилиты ----------

func (a *App) handleMediaUpload(msg *tgbotapi.Message, sess *session.Session) {
	nomineeID := sess.WaitingMediaForNomineeID
	sess.WaitingMediaForNomineeID = 0
	if nomineeID == 0 {
		return
	}

	ok, err := a.store.IsNomineeOwner(nomineeID, msg.From.ID)
	if err != nil {
		log.Println("isNomineeOwner(media):", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки прав."))
		return
	}
	if !ok {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Только автор комнаты может менять медиа у номинантов."))
		return
	}

	var fileID, mediaType string
	if len(msg.Photo) > 0 {
		photo := msg.Photo[len(msg.Photo)-1]
		fileID = photo.FileID
		mediaType = "photo"
	} else if msg.Video != nil {
		fileID = msg.Video.FileID
		mediaType = "video"
	} else {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Нужно отправить фото или видео. Команда /set_nominee_media nomineeID."))
		return
	}

	if err := a.store.UpdateNomineeMedia(nomineeID, fileID, mediaType); err != nil {
		log.Println("update nominee media:", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не удалось сохранить медиа."))
		return
	}

	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Медиа для номинанта сохранено ✅"))
}

func (a *App) handleCreateNomineeTextStep(msg *tgbotapi.Message, sess *session.Session) {
	nominationID := sess.CreatingNomineeForNominationID
	if nominationID == 0 {
		return
	}

	name := strings.TrimSpace(msg.Text)
	if name == "" {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Имя номинанта не может быть пустым. Отправь текстом имя."))
		return
	}

	// сбрасываем флаг создания (чтобы не зациклиться)
	sess.CreatingNomineeForNominationID = 0

	ok, err := a.store.IsNominationOwner(nominationID, msg.From.ID)
	if err != nil {
		log.Println("IsNominationOwner(create nominee text):", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Ошибка проверки прав."))
		return
	}
	if !ok {
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Только автор комнаты может добавлять номинантов."))
		return
	}

	nomineeID, err := a.store.CreateNominee(nominationID, name)
	if err != nil {
		log.Println("CreateNominee(create nominee text):", err)
		a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, "Не удалось создать номинанта."))
		return
	}

	sess.WaitingMediaForNomineeID = nomineeID

	text := fmt.Sprintf("Номинант «%s» добавлен ✅\nТеперь отправь одним сообщением фото или видео для него (опционально).", name)
	a.bot.Send(tgbotapi.NewMessage(msg.Chat.ID, text))
}

func splitPipeArgs(s string, n int) []string {
	raw := strings.SplitN(s, "|", n)
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		p := strings.TrimSpace(part)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (a *App) sendNominationsList(chatID, userID, roomID int64) error {
	nominations, err := a.store.ListNominations(roomID)
	if err != nil {
		return err
	}

	if len(nominations) == 0 {
		_, sendErr := a.bot.Send(tgbotapi.NewMessage(chatID, "В этой комнате пока нет номинаций."))
		return sendErr
	}

	isOwner, err := a.store.IsRoomOwner(roomID, userID)
	if err != nil {
		log.Println("IsRoomOwner in sendNominationsList:", err)
		isOwner = false
	}

	var buttons [][]tgbotapi.InlineKeyboardButton
	var sb strings.Builder

	sb.WriteString("Список номинаций в комнате:\n")
	for _, n := range nominations {
		sb.WriteString(fmt.Sprintf("ID %d — %s\n", n.ID, n.Name))

		openData := fmt.Sprintf("nomination:%d", n.ID)
		openBtn := tgbotapi.NewInlineKeyboardButtonData("🗳 Открыть", openData)

		if isOwner {
			resData := fmt.Sprintf("res_nom:%d", n.ID)
			resBtn := tgbotapi.NewInlineKeyboardButtonData("📊 Результаты", resData)
			buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(openBtn, resBtn))
		} else {
			buttons = append(buttons, tgbotapi.NewInlineKeyboardRow(openBtn))
		}
	}

	sb.WriteString("\nЭти ID можно использовать в командах:\n")
	sb.WriteString("/add_nominee nominationID | Имя\n")
	sb.WriteString("/delete_nomination nominationID\n")
	sb.WriteString("/results nominationID\n")

	kb := tgbotapi.NewInlineKeyboardMarkup(buttons...)
	msg := tgbotapi.NewMessage(chatID, sb.String())
	msg.ReplyMarkup = kb
	_, err = a.bot.Send(msg)
	return err
}

func (a *App) sendNominees(chatID, userID, nominationID int64) error {
	nominationName, err := a.store.GetNominationName(nominationID)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		log.Println("get nomination name:", err)
	}

	nominees, err := a.store.ListNominees(nominationID)
	if err != nil {
		log.Println("ListNominees:", err)
		a.bot.Send(tgbotapi.NewMessage(chatID, "Не удалось получить список номинантов 😔"))
		return err
	}

	isOwner, err := a.store.IsNominationOwner(nominationID, userID)
	if err != nil {
		log.Println("IsNominationOwner(sendNominees):", err)
		isOwner = false
	}

	// заголовок
	if nominationName != "" {
		a.bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("🏆 Номинация: %s (ID %d)", nominationName, nominationID)))
	} else {
		a.bot.Send(tgbotapi.NewMessage(chatID, fmt.Sprintf("🏆 Номинация ID %d", nominationID)))
	}

	// отдельная кнопка "➕ Добавить номинанта" для владельца
	if isOwner {
		kb := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("➕ Добавить номинанта", fmt.Sprintf("addnom:%d", nominationID)),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад к номинациям", "back:nominations"),
			),
		)
		msg := tgbotapi.NewMessage(chatID, "Управление номинацией:")
		msg.ReplyMarkup = kb
		if _, err := a.bot.Send(msg); err != nil {
			log.Println("send addnom button:", err)
		}
	}

	if len(nominees) == 0 {
		m := tgbotapi.NewMessage(chatID, "В этой номинации пока нет номинантов.")
		m.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад к номинациям", "back:nominations"),
			),
		)
		_, sendErr := a.bot.Send(m)
		return sendErr
	}

	for _, n := range nominees {
		voteData := fmt.Sprintf("vote:%d", n.ID)
		voteRow := tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✅ Голосовать", voteData),
		)

		rows := [][]tgbotapi.InlineKeyboardButton{voteRow}

		// если владелец комнаты — добавляем кнопки "Медиа" и "Удалить"
		if isOwner {
			adminRow := tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🖼 Медиа", fmt.Sprintf("setmedia:%d", n.ID)),
				tgbotapi.NewInlineKeyboardButtonData("🗑 Удалить", fmt.Sprintf("delnom:%d", n.ID)),
			)
			rows = append(rows, adminRow)
		}

		// навигация "назад" всегда доступна (и пользователям, и админам)
		backRow := tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("⬅️ Назад к номинациям", "back:nominations"),
		)
		rows = append(rows, backRow)

		kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
		caption := fmt.Sprintf("ID %d — %s\n\nНажми кнопку, чтобы отдать голос.", n.ID, n.Name)

		if n.MediaFileID != "" && n.MediaType == "photo" {
			photo := tgbotapi.NewPhoto(chatID, tgbotapi.FileID(n.MediaFileID))
			photo.Caption = caption
			photo.ReplyMarkup = kb
			if _, err := a.bot.Send(photo); err != nil {
				log.Println("send nominee photo:", err)
			}
		} else if n.MediaFileID != "" && n.MediaType == "video" {
			video := tgbotapi.NewVideo(chatID, tgbotapi.FileID(n.MediaFileID))
			video.Caption = caption
			video.ReplyMarkup = kb
			if _, err := a.bot.Send(video); err != nil {
				log.Println("send nominee video:", err)
			}
		} else {
			msg := tgbotapi.NewMessage(chatID, caption)
			msg.ReplyMarkup = kb
			if _, err := a.bot.Send(msg); err != nil {
				log.Println("send nominee text:", err)
			}
		}
	}

	return nil
}
