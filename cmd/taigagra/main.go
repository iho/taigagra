package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mymmrac/telego"
	th "github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/iho/taigagra/internal/config"
	"github.com/iho/taigagra/internal/storage"
	"github.com/iho/taigagra/internal/taiga"
)

type newWizardState struct {
	ProjectID    int64
	AssigneeID   *int64
	AwaitingText bool
}

var (
	newWizardMu sync.Mutex
	newWizard   = make(map[int64]newWizardState)
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	store, err := storage.New(cfg.StoragePath)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}

	bot, err := telego.NewBot(cfg.TelegramToken)
	if err != nil {
		log.Fatalf("create bot: %v", err)
	}

	updates, err := bot.UpdatesViaLongPolling(ctx, nil)
	if err != nil {
		log.Fatalf("start long polling: %v", err)
	}

	bh, err := th.NewBotHandler(bot, updates)
	if err != nil {
		log.Fatalf("create bot handler: %v", err)
	}
	defer func() { _ = bh.Stop() }()

	bh.Handle(func(ctx *th.Context, update telego.Update) error {
		if update.Message != nil && update.Message.From != nil {
			_ = store.UpsertTelegramUsername(update.Message.From.Username, update.Message.From.ID)
		}
		if update.CallbackQuery != nil {
			_ = store.UpsertTelegramUsername(update.CallbackQuery.From.Username, update.CallbackQuery.From.ID)
		}
		return ctx.Next(update)
	})

	resolveTelegramTarget := func(raw string) (int64, error) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return 0, fmt.Errorf("потрібен користувач Telegram")
		}
		if strings.HasPrefix(raw, "@") {
			id, ok := store.ResolveTelegramHandle(raw)
			if !ok {
				return 0, fmt.Errorf("не знаю цього @username: %s (користувач має хоч раз написати боту/в чаті)", raw)
			}
			return id, nil
		}
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id == 0 {
			return 0, fmt.Errorf("некоректний id користувача Telegram")
		}
		return id, nil
	}

	isProjectAdmin := func(ctx context.Context, telegramID int64, projectID int64) (bool, error) {
		link, ok := store.Get(telegramID)
		if !ok {
			return false, fmt.Errorf("Немає привʼязки. Використай /link <taiga_token>.")
		}
		client, err := taiga.NewClient(cfg.TaigaBaseURL, link.TaigaToken)
		if err != nil {
			return false, err
		}
		memberships, err := client.ListMemberships(ctx, projectID)
		if err != nil {
			return false, err
		}
		for _, m := range memberships {
			if m.UserID != link.TaigaUserID {
				continue
			}
			return m.IsAdmin || m.IsOwner, nil
		}
		return false, nil
	}

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return sendText(ctx, message.Chat.ID, "Команди:\n/link <taiga_token>\n/me\n/unlink\n/projects\n/new\n/cancel\n/notifyhere\n/notifychat <chat_id>\n/notifypm\n/watch <project_id>\n/unwatch <project_id>\n/watches\n/map <project_id> <taiga_user_id>  (reply)\n/mapid <project_id> <telegram_user_id> <taiga_user_id>\n/mappings <project_id>\n/adminlinkid <project_id> <telegram_user_id> <taiga_token>\n/task <project_id> [taiga_user_id] <subject> [| description]  (створює завдання)\n/taskto <project_id> <taiga_user_id> <subject> [| description]  (створює завдання)\n/my [project_id]  (показує user stories)")
	}, th.CommandEqual("start"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		if message.Chat.Type != "private" {
			return sendText(ctx, message.Chat.ID, "Цю команду можна використовувати лише в приватному чаті")
		}

		args := strings.TrimSpace(commandArgs(message.Text))
		parts := strings.Fields(args)
		if len(parts) != 3 {
			return sendText(ctx, message.Chat.ID, "Використання: /adminlinkid <project_id> <telegram_user_id|@username> <taiga_token>")
		}
		projectID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || projectID <= 0 {
			return sendText(ctx, message.Chat.ID, "Некоректний id проєкту")
		}
		targetTelegramID, err := resolveTelegramTarget(parts[1])
		if err != nil {
			return sendText(ctx, message.Chat.ID, err.Error())
		}
		taigaToken := parts[2]
		if strings.TrimSpace(taigaToken) == "" {
			return sendText(ctx, message.Chat.ID, "Потрібен taiga_token")
		}

		admin, err := isProjectAdmin(ctx, message.From.ID, projectID)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка перевірки прав: %v", err))
		}
		if !admin {
			return sendText(ctx, message.Chat.ID, "Недостатньо прав: потрібен адміністратор проєкту в Taiga")
		}

		taigaClient, err := taiga.NewClient(cfg.TaigaBaseURL, taigaToken)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка клієнта Taiga: %v", err))
		}
		me, err := taigaClient.GetMe(ctx)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося перевірити taiga_token: %v", err))
		}

		link := storage.UserLink{
			TelegramID:    targetTelegramID,
			TaigaToken:    taigaToken,
			TaigaUserID:   me.ID,
			TaigaUserName: me.FullName,
		}
		if err := store.Save(link); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося зберегти привʼязку: %v", err))
		}

		_ = ctx.Bot().DeleteMessage(ctx, &telego.DeleteMessageParams{ChatID: tu.ID(message.Chat.ID), MessageID: message.MessageID})
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Збережено привʼязку для Telegram %d -> Taiga %d", targetTelegramID, me.ID))
	}, th.CommandEqual("adminlinkid"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		args := strings.TrimSpace(commandArgs(message.Text))
		parts := strings.Fields(args)
		if len(parts) != 2 {
			return sendText(ctx, message.Chat.ID, "Використання: /map <project_id> <taiga_user_id> (відповіддю на повідомлення користувача)")
		}
		if message.ReplyToMessage == nil || message.ReplyToMessage.From == nil {
			return sendText(ctx, message.Chat.ID, "Команду /map потрібно надсилати у відповідь на повідомлення користувача")
		}
		projectID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || projectID <= 0 {
			return sendText(ctx, message.Chat.ID, "Некоректний id проєкту")
		}
		taigaUserID, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || taigaUserID <= 0 {
			return sendText(ctx, message.Chat.ID, "Некоректний id користувача Taiga")
		}

		admin, err := isProjectAdmin(ctx, message.From.ID, projectID)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка перевірки прав: %v", err))
		}
		if !admin {
			return sendText(ctx, message.Chat.ID, "Недостатньо прав: потрібен адміністратор проєкту в Taiga")
		}

		targetTelegramID := message.ReplyToMessage.From.ID
		if err := store.SetProjectUserMapping(projectID, targetTelegramID, taigaUserID); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося зберегти мапінг: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Збережено мапінг: Telegram %d -> Taiga %d (проєкт %d)", targetTelegramID, taigaUserID, projectID))
	}, th.CommandEqual("map"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		args := strings.TrimSpace(commandArgs(message.Text))
		parts := strings.Fields(args)
		if len(parts) != 3 {
			return sendText(ctx, message.Chat.ID, "Використання: /mapid <project_id> <telegram_user_id|@username> <taiga_user_id>")
		}
		projectID, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || projectID <= 0 {
			return sendText(ctx, message.Chat.ID, "Некоректний id проєкту")
		}
		targetTelegramID, err := resolveTelegramTarget(parts[1])
		if err != nil {
			return sendText(ctx, message.Chat.ID, err.Error())
		}
		taigaUserID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || taigaUserID <= 0 {
			return sendText(ctx, message.Chat.ID, "Некоректний id користувача Taiga")
		}

		admin, err := isProjectAdmin(ctx, message.From.ID, projectID)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка перевірки прав: %v", err))
		}
		if !admin {
			return sendText(ctx, message.Chat.ID, "Недостатньо прав: потрібен адміністратор проєкту в Taiga")
		}
		if err := store.SetProjectUserMapping(projectID, targetTelegramID, taigaUserID); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося зберегти мапінг: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Збережено мапінг: Telegram %d -> Taiga %d (проєкт %d)", targetTelegramID, taigaUserID, projectID))
	}, th.CommandEqual("mapid"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		args := strings.TrimSpace(commandArgs(message.Text))
		projectID, err := strconv.ParseInt(args, 10, 64)
		if err != nil || projectID <= 0 {
			return sendText(ctx, message.Chat.ID, "Використання: /mappings <project_id>")
		}

		admin, err := isProjectAdmin(ctx, message.From.ID, projectID)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка перевірки прав: %v", err))
		}
		if !admin {
			return sendText(ctx, message.Chat.ID, "Недостатньо прав: потрібен адміністратор проєкту в Taiga")
		}

		m := store.ListProjectUserMappings(projectID)
		if len(m) == 0 {
			return sendText(ctx, message.Chat.ID, "Немає мапінгів")
		}
		ids := make([]int64, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		var b strings.Builder
		b.WriteString(fmt.Sprintf("Мапінги для проєкту %d:\n", projectID))
		for _, tgID := range ids {
			b.WriteString(fmt.Sprintf("Telegram %d -> Taiga %d\n", tgID, m[tgID]))
		}
		return sendText(ctx, message.Chat.ID, b.String())
	}, th.CommandEqual("mappings"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		link, ok := store.Get(message.From.ID)
		if !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}

		client, err := taiga.NewClient(cfg.TaigaBaseURL, link.TaigaToken)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка клієнта Taiga: %v", err))
		}
		projects, err := client.ListProjects(context.Background())
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося отримати список проєктів: %v", err))
		}
		if len(projects) == 0 {
			return sendText(ctx, message.Chat.ID, "Немає проєктів")
		}

		rows := make([][]telego.InlineKeyboardButton, 0, len(projects))
		for _, p := range projects {
			data := fmt.Sprintf("new:proj:%d", p.ID)
			rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(p.Name).WithCallbackData(data)))
		}
		rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Скасувати").WithCallbackData("new:cancel")))

		_, err = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(message.Chat.ID), "Обери проєкт:").WithReplyMarkup(tu.InlineKeyboard(rows...)))
		return err
	}, th.CommandEqual("new"))

	bh.HandleCallbackQuery(func(ctx *th.Context, query telego.CallbackQuery) error {
		if query.From.ID == 0 {
			return nil
		}
		if query.Message == nil {
			return nil
		}
		msg, ok := query.Message.(*telego.Message)
		if !ok {
			_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Повідомлення недоступне"))
			return nil
		}

		chatID := msg.Chat.ID
		telegramID := query.From.ID
		data := query.Data

		deleteInlineMessage := func() {
			_ = ctx.Bot().DeleteMessage(ctx, &telego.DeleteMessageParams{
				ChatID:    tu.ID(chatID),
				MessageID: msg.MessageID,
			})
		}

		if data == "new:cancel" {
			deleteInlineMessage()
			newWizardMu.Lock()
			delete(newWizard, telegramID)
			newWizardMu.Unlock()
			_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Скасовано"))
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), "Скасовано"))
			return nil
		}

		parts := strings.Split(data, ":")
		if len(parts) < 3 {
			_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Некоректні дані"))
			return nil
		}
		if parts[0] != "new" {
			return nil
		}

		switch parts[1] {
		case "proj":
			deleteInlineMessage()
			projectID, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil || projectID <= 0 {
				_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Некоректний проєкт"))
				return nil
			}

			newWizardMu.Lock()
			newWizard[telegramID] = newWizardState{ProjectID: projectID}
			newWizardMu.Unlock()

			link, ok := store.Get(telegramID)
			if !ok {
				_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Немає привʼязки"))
				return nil
			}
			client, err := taiga.NewClient(cfg.TaigaBaseURL, link.TaigaToken)
			if err != nil {
				_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Помилка"))
				_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), fmt.Sprintf("Помилка клієнта Taiga: %v", err)))
				return nil
			}
			memberships, err := client.ListMemberships(context.Background(), projectID)
			if err != nil {
				_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Помилка"))
				_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), fmt.Sprintf("Не вдалося отримати користувачів проєкту: %v", err)))
				return nil
			}

			assignees := make(map[int64]string)
			for _, m := range memberships {
				name := strings.TrimSpace(m.FullName)
				if name == "" {
					name = fmt.Sprintf("%d", m.UserID)
				}
				assignees[m.UserID] = name
			}
			ids := make([]int64, 0, len(assignees))
			for id := range assignees {
				ids = append(ids, id)
			}
			sort.Slice(ids, func(i, j int) bool { return assignees[ids[i]] < assignees[ids[j]] })

			rows := make([][]telego.InlineKeyboardButton, 0, len(ids)+2)
			rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Без виконавця").WithCallbackData(fmt.Sprintf("new:assignee:%d:0", projectID))))
			for _, id := range ids {
				data := fmt.Sprintf("new:assignee:%d:%d", projectID, id)
				rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton(assignees[id]).WithCallbackData(data)))
			}
			rows = append(rows, tu.InlineKeyboardRow(tu.InlineKeyboardButton("Скасувати").WithCallbackData("new:cancel")))
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), "Обери виконавця:").WithReplyMarkup(tu.InlineKeyboard(rows...)))
			_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Ок"))
			return nil

		case "assignee":
			deleteInlineMessage()
			if len(parts) < 4 {
				_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Некоректні дані"))
				return nil
			}
			projectID, err := strconv.ParseInt(parts[2], 10, 64)
			if err != nil || projectID <= 0 {
				_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Некоректний проєкт"))
				return nil
			}
			assigneeRaw, err := strconv.ParseInt(parts[3], 10, 64)
			if err != nil || assigneeRaw < 0 {
				_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Некоректний виконавець"))
				return nil
			}

			var assigneeID *int64
			if assigneeRaw != 0 {
				a := assigneeRaw
				assigneeID = &a
			}

			newWizardMu.Lock()
			newWizard[telegramID] = newWizardState{ProjectID: projectID, AssigneeID: assigneeID, AwaitingText: true}
			newWizardMu.Unlock()

			_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Ок"))
			_, _ = ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), "Введи тему та (необовʼязково) опис у форматі: Тема | опис"))
			return nil
		}

		_ = ctx.Bot().AnswerCallbackQuery(ctx, tu.CallbackQuery(query.ID).WithText("Невідома дія"))
		return nil
	}, th.AnyCallbackQueryWithMessage(), th.CallbackDataPrefix("new:"))

	notCommand := func(_ context.Context, update telego.Update) bool {
		if update.Message == nil {
			return false
		}
		text := strings.TrimSpace(update.Message.Text)
		if text == "" {
			return false
		}
		return !strings.HasPrefix(text, "/")
	}

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return nil
		}

		newWizardMu.Lock()
		state, ok := newWizard[message.From.ID]
		newWizardMu.Unlock()
		if !ok || !state.AwaitingText {
			return nil
		}

		subject, description := splitSubjectDescription(strings.TrimSpace(message.Text))
		if strings.TrimSpace(subject) == "" {
			return sendText(ctx, message.Chat.ID, "Потрібна тема")
		}

		link, ok := store.Get(message.From.ID)
		if !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		client, err := taiga.NewClient(cfg.TaigaBaseURL, link.TaigaToken)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка клієнта Taiga: %v", err))
		}

		req := taiga.UserStoryCreateRequest{
			ProjectID:   state.ProjectID,
			Subject:     subject,
			Description: description,
			Assigned:    state.AssigneeID,
		}
		us, err := client.CreateUserStory(context.Background(), req)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося створити завдання: %v", err))
		}

		newWizardMu.Lock()
		delete(newWizard, message.From.ID)
		newWizardMu.Unlock()

		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Створено завдання #%d: %s", us.Ref, us.Subject))
	}, notCommand)

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		args := strings.TrimSpace(commandArgs(message.Text))
		if args == "" {
			return sendText(ctx, message.Chat.ID, "Використання: /link <taiga_token>")
		}
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Не можу привʼязати: відсутня інформація про користувача")
		}

		taigaToken := args
		client, err := taiga.NewClient(cfg.TaigaBaseURL, taigaToken)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка клієнта Taiga: %v", err))
		}

		me, err := client.GetMe(context.Background())
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка авторизації в Taiga: %v", err))
		}

		link := storage.UserLink{
			TelegramID:     message.From.ID,
			TaigaToken:     taigaToken,
			TaigaUserID:    me.ID,
			TaigaUserName:  me.FullName,
			LastTaskStates: nil,
		}
		if err := store.Save(link); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося зберегти привʼязку: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Привʼязано до користувача Taiga: %s (%d)", me.FullName, me.ID))
	}, th.CommandEqual("link"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		link, ok := store.Get(message.From.ID)
		if !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Привʼязаний користувач Taiga: %s (%d)", link.TaigaUserName, link.TaigaUserID))
	}, th.CommandEqual("me"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		link, ok := store.Get(message.From.ID)
		if !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		client, err := taiga.NewClient(cfg.TaigaBaseURL, link.TaigaToken)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка клієнта Taiga: %v", err))
		}
		projects, err := client.ListProjects(context.Background())
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося отримати список проєктів: %v", err))
		}
		if len(projects) == 0 {
			return sendText(ctx, message.Chat.ID, "Немає проєктів")
		}
		var b strings.Builder
		for _, p := range projects {
			b.WriteString(fmt.Sprintf("%d %s (%s)\n", p.ID, p.Name, p.Slug))
		}
		return sendText(ctx, message.Chat.ID, b.String())
	}, th.CommandEqual("projects"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		if err := store.Delete(message.From.ID); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося відвʼязати: %v", err))
		}
		return sendText(ctx, message.Chat.ID, "Відвʼязано")
	}, th.CommandEqual("unlink"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		if _, ok := store.Get(message.From.ID); !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		chatID := message.Chat.ID
		if err := store.SetNotifyChat(message.From.ID, &chatID); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося встановити чат для сповіщень: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Сповіщення надсилатимуться сюди (%d)", message.Chat.ID))
	}, th.CommandEqual("notifyhere"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		if _, ok := store.Get(message.From.ID); !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		chatID, err := parseChatID(commandArgs(message.Text))
		if err != nil {
			return sendText(ctx, message.Chat.ID, err.Error())
		}
		if err := store.SetNotifyChat(message.From.ID, &chatID); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося встановити чат для сповіщень: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Сповіщення надсилатимуться в чат %d", chatID))
	}, th.CommandEqual("notifychat"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		if _, ok := store.Get(message.From.ID); !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		if err := store.SetNotifyChat(message.From.ID, nil); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося скинути чат для сповіщень: %v", err))
		}
		return sendText(ctx, message.Chat.ID, "Сповіщення надсилатимуться в приватний чат")
	}, th.CommandEqual("notifypm"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		if _, ok := store.Get(message.From.ID); !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		projectID, err := parseRequiredProjectID(commandArgs(message.Text))
		if err != nil {
			return sendText(ctx, message.Chat.ID, err.Error())
		}
		if err := store.AddWatchedProject(message.From.ID, projectID); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося підписатися: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Підписано на проєкт %d", projectID))
	}, th.CommandEqual("watch"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		if _, ok := store.Get(message.From.ID); !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		projectID, err := parseRequiredProjectID(commandArgs(message.Text))
		if err != nil {
			return sendText(ctx, message.Chat.ID, err.Error())
		}
		if err := store.RemoveWatchedProject(message.From.ID, projectID); err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося відписатися: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Відписано від проєкту %d", projectID))
	}, th.CommandEqual("unwatch"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		link, ok := store.Get(message.From.ID)
		if !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}
		if len(link.WatchedProjects) == 0 {
			return sendText(ctx, message.Chat.ID, "Немає проєктів у підписках")
		}
		var b strings.Builder
		b.WriteString("Підписки на проєкти:\n")
		for _, pid := range link.WatchedProjects {
			b.WriteString(fmt.Sprintf("%d\n", pid))
		}
		return sendText(ctx, message.Chat.ID, b.String())
	}, th.CommandEqual("watches"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		link, ok := store.Get(message.From.ID)
		if !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}

		projectID, assigneeID, subject, description, err := parseTaskTo(commandArgs(message.Text))
		if err != nil {
			return sendText(ctx, message.Chat.ID, err.Error())
		}

		client, err := taiga.NewClient(cfg.TaigaBaseURL, link.TaigaToken)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка клієнта Taiga: %v", err))
		}

		req := taiga.UserStoryCreateRequest{
			ProjectID:   projectID,
			Subject:     subject,
			Description: description,
			Assigned:    &assigneeID,
		}

		us, err := client.CreateUserStory(context.Background(), req)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося створити завдання: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Створено завдання #%d: %s", us.Ref, us.Subject))
	}, th.CommandEqual("taskto"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		link, ok := store.Get(message.From.ID)
		if !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}

		projectID, assigneeID, subject, description, err := parseTaskWithOptionalAssignee(commandArgs(message.Text))
		if err != nil {
			return sendText(ctx, message.Chat.ID, err.Error())
		}

		client, err := taiga.NewClient(cfg.TaigaBaseURL, link.TaigaToken)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка клієнта Taiga: %v", err))
		}

		if assigneeID == nil {
			assigneeID = &link.TaigaUserID
		}
		req := taiga.UserStoryCreateRequest{
			ProjectID:   projectID,
			Subject:     subject,
			Description: description,
			Assigned:    assigneeID,
		}

		us, err := client.CreateUserStory(context.Background(), req)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося створити завдання: %v", err))
		}
		return sendText(ctx, message.Chat.ID, fmt.Sprintf("Створено завдання #%d: %s", us.Ref, us.Subject))
	}, th.CommandEqual("task"))

	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		if message.From == nil {
			return sendText(ctx, message.Chat.ID, "Відсутня інформація про користувача")
		}
		link, ok := store.Get(message.From.ID)
		if !ok {
			return sendText(ctx, message.Chat.ID, "Немає привʼязки. Використай /link <taiga_token>.")
		}

		projectID, err := parseOptionalProjectID(commandArgs(message.Text))
		if err != nil {
			return sendText(ctx, message.Chat.ID, err.Error())
		}

		client, err := taiga.NewClient(cfg.TaigaBaseURL, link.TaigaToken)
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Помилка клієнта Taiga: %v", err))
		}

		assigned := link.TaigaUserID
		stories, err := client.ListUserStories(context.Background(), taiga.ListUserStoriesParams{ProjectID: projectID, AssignedTo: &assigned})
		if err != nil {
			return sendText(ctx, message.Chat.ID, fmt.Sprintf("Не вдалося отримати список user stories: %v", err))
		}

		if len(stories) == 0 {
			return sendText(ctx, message.Chat.ID, "Немає user stories")
		}

		var b strings.Builder
		for _, us := range stories {
			b.WriteString(fmt.Sprintf("#%d %s [%s]\n", us.Ref, us.Subject, us.StatusExtraInfo.Name))
		}
		return sendText(ctx, message.Chat.ID, b.String())
	}, th.CommandEqual("my"))

	go pollNotifications(ctx, bot, store, cfg.TaigaBaseURL, cfg.PollInterval)

	if err := bh.Start(); err != nil {
		log.Fatalf("start handler: %v", err)
	}
}

func sendText(ctx *th.Context, chatID int64, text string) error {
	if text == "" {
		return nil
	}
	for _, chunk := range splitMessage(text, 3500) {
		_, err := ctx.Bot().SendMessage(ctx, tu.Message(tu.ID(chatID), chunk))
		if err != nil {
			return err
		}
	}
	return nil
}

func splitMessage(text string, limit int) []string {
	if limit <= 0 {
		return []string{text}
	}

	runes := []rune(text)
	if len(runes) <= limit {
		return []string{text}
	}

	parts := make([]string, 0, (len(runes)/limit)+1)
	for len(runes) > 0 {
		if len(runes) <= limit {
			parts = append(parts, strings.TrimSpace(string(runes)))
			break
		}

		window := runes[:limit]
		cut := len(window)
		for i := len(window) - 1; i >= 0; i-- {
			if window[i] == '\n' {
				cut = i + 1
				break
			}
		}
		chunk := strings.TrimSpace(string(runes[:cut]))
		if chunk != "" {
			parts = append(parts, chunk)
		}
		runes = runes[cut:]
	}

	return parts
}

func commandArgs(text string) string {
	if text == "" {
		return ""
	}
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

func parseOptionalProjectID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	projectID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("некоректний id проєкту")
	}
	return projectID, nil
}

func parseChatID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("потрібен id чату")
	}
	chatID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("некоректний id чату")
	}
	return chatID, nil
}

func parseTaskWithOptionalAssignee(raw string) (projectID int64, assigneeID *int64, subject string, description string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil, "", "", fmt.Errorf("Використання: /task <project_id> [taiga_user_id] <subject> [| description]")
	}

	fields := strings.Fields(raw)
	if len(fields) < 2 {
		return 0, nil, "", "", fmt.Errorf("Використання: /task <project_id> [taiga_user_id] <subject> [| description]")
	}

	projectID, err = strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, nil, "", "", fmt.Errorf("некоректний id проєкту")
	}

	remaining := strings.TrimSpace(strings.TrimPrefix(raw, fields[0]))

	if len(fields) >= 3 {
		candidate, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr == nil {
			assigneeID = &candidate
			remaining = strings.TrimSpace(strings.TrimPrefix(remaining, fields[1]))
		}
	}

	subject, description = splitSubjectDescription(strings.TrimSpace(remaining))
	if subject == "" {
		return 0, nil, "", "", fmt.Errorf("потрібна тема")
	}

	return projectID, assigneeID, subject, description, nil
}

func parseRequiredProjectID(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("потрібен id проєкту")
	}
	projectID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("некоректний id проєкту")
	}
	if projectID <= 0 {
		return 0, fmt.Errorf("некоректний id проєкту")
	}
	return projectID, nil
}

func parseTask(raw string) (projectID int64, subject string, description string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, "", "", fmt.Errorf("Використання: /task <project_id> <subject> [| description]")
	}

	parts := strings.SplitN(raw, " ", 2)
	if len(parts) < 2 {
		return 0, "", "", fmt.Errorf("Використання: /task <project_id> <subject> [| description]")
	}

	projectID, err = strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, "", "", fmt.Errorf("некоректний id проєкту")
	}

	subject, description = splitSubjectDescription(strings.TrimSpace(parts[1]))
	if subject == "" {
		return 0, "", "", fmt.Errorf("потрібна тема")
	}

	return projectID, subject, description, nil
}

func parseTaskTo(raw string) (projectID int64, assigneeID int64, subject string, description string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, 0, "", "", fmt.Errorf("Використання: /taskto <project_id> <taiga_user_id> <subject> [| description]")
	}

	parts := strings.Fields(raw)
	if len(parts) < 3 {
		return 0, 0, "", "", fmt.Errorf("Використання: /taskto <project_id> <taiga_user_id> <subject> [| description]")
	}

	projectID, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("некоректний id проєкту")
	}

	assigneeID, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, "", "", fmt.Errorf("некоректний id користувача Taiga")
	}

	rest := strings.TrimSpace(strings.TrimPrefix(raw, parts[0]))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, parts[1]))

	subject, description = splitSubjectDescription(strings.TrimSpace(rest))
	if subject == "" {
		return 0, 0, "", "", fmt.Errorf("потрібна тема")
	}

	return projectID, assigneeID, subject, description, nil
}

func splitSubjectDescription(raw string) (subject, description string) {
	if raw == "" {
		return "", ""
	}

	parts := strings.SplitN(raw, "|", 2)
	subject = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		description = strings.TrimSpace(parts[1])
	}
	return subject, description
}

func pollNotifications(ctx context.Context, bot *telego.Bot, store *storage.Store, taigaBaseURL string, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			links := store.List()
			for _, link := range links {
				last := link.LastTaskStates
				if last == nil {
					last = make(map[int64]storage.TaskDigest)
				}
				destinationChatID := link.TelegramID
				if link.NotifyChatID != nil {
					destinationChatID = *link.NotifyChatID
				}
				baselineOnly := len(last) == 0
				client, err := taiga.NewClient(taigaBaseURL, link.TaigaToken)
				if err != nil {
					continue
				}

				allStories := make(map[int64]taiga.UserStory)
				assigned := link.TaigaUserID
				storiesAssigned, err := client.ListUserStories(context.Background(), taiga.ListUserStoriesParams{AssignedTo: &assigned})
				if err == nil {
					for _, us := range storiesAssigned {
						allStories[us.ID] = us
					}
				}
				for _, projectID := range link.WatchedProjects {
					storiesProject, err := client.ListUserStories(context.Background(), taiga.ListUserStoriesParams{ProjectID: projectID})
					if err != nil {
						continue
					}
					for _, us := range storiesProject {
						allStories[us.ID] = us
					}
				}

				newDigests := make(map[int64]storage.TaskDigest, len(allStories))
				for _, us := range allStories {
					assignedTo := int64(0)
					if us.AssignedTo != nil {
						assignedTo = *us.AssignedTo
					}

					digest := storage.TaskDigest{
						Status:     us.StatusExtraInfo.Name,
						AssignedTo: assignedTo,
					}
					newDigests[us.ID] = digest
					if baselineOnly {
						continue
					}

					old, ok := last[us.ID]
					if !ok {
						continue
					}
					if old.Status != digest.Status {
						_, _ = bot.SendMessage(ctx, tu.Message(tu.ID(destinationChatID), fmt.Sprintf("Статус завдання змінено: #%d %s (%s -> %s)", us.Ref, us.Subject, old.Status, digest.Status)))
						continue
					}
					if old.AssignedTo != digest.AssignedTo {
						_, _ = bot.SendMessage(ctx, tu.Message(tu.ID(destinationChatID), fmt.Sprintf("Виконавця завдання змінено: #%d %s", us.Ref, us.Subject)))
						continue
					}
				}

				_ = store.UpdateTaskState(link.TelegramID, newDigests)
			}
		}
	}
}
