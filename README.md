# Job Intel Bot

Telegram-бот для мониторинга вакансий на [Habr Career](https://career.habr.com) и [HeadHunter](https://hh.ru).
Периодически проверяет новые вакансии по сохранённым фильтрам и уведомляет пользователей о новых совпадениях.

## Возможности

- Подписка на ключевые слова с выбором источника (Habr Career / HeadHunter)
- Фильтрация по опыту, формату работы, зарплате, валюте, периоду публикации (HH)
- HH: за один цикл забирается первая страница (до 50 вакансий), отсортированная по дате публикации — самые свежие первыми
- Дедупликация: повторные вакансии не присылаются
- Управление подписками через команды Telegram (удаление и редактирование — через inline-кнопки без ввода ID)
- Поддержка нескольких независимых подписок на одного пользователя
- Запуск через HTTP/SOCKS5 прокси (для регионов без прямого доступа к Telegram)

## Требования

- Go 1.21+
- Telegram Bot Token (получить у [@BotFather](https://t.me/BotFather))

## Установка и запуск

```bash
git clone https://github.com/NodeLie/job_intel_bot
cd job-intel-bot
go build -o bot ./cmd/bot
```

Скопируй шаблон конфига и заполни:

```bash
cp config.yaml.example config.yaml  # если есть шаблон
# или создай config.yaml вручную
```

Запуск:

```bash
./bot
```

## Конфигурация

Настройки читаются в следующем порядке (каждый следующий уровень перекрывает предыдущий):

1. Значения по умолчанию
2. `config.yaml` в рабочей директории
3. Переменные окружения

### config.yaml

```yaml
# Job Intel Bot configuration

bot_token: "your-telegram-bot-token"  # обязательно
subs_path: "subscriptions.json"       # путь к файлу подписок
seen_path: "seen_jobs.json"           # путь к файлу просмотренных вакансий
poll_interval: "30m"                  # интервал проверки (например: 5m, 1h)
log_level: "info"                     # уровень логов: debug|info|warn|error
db_path: "./job-intel-bot.db"         # путь к SQLite БД (зарезервировано)
# proxy_url: ""                       # прокси для Telegram: socks5://user:pass@host:port
```

> `config.yaml` добавлен в `.gitignore` — токен не попадёт в репозиторий.

### Переменные окружения

Все поля конфига можно переопределить через env vars (удобно для CI/Docker):

| Переменная      | Поле конфига   | По умолчанию         |
|-----------------|----------------|----------------------|
| `BOT_TOKEN`     | `bot_token`    | —                    |
| `SUBS_PATH`     | `subs_path`    | `subscriptions.json` |
| `SEEN_PATH`     | `seen_path`    | `seen_jobs.json`     |
| `POLL_INTERVAL` | `poll_interval`| `30m`                |
| `LOG_LEVEL`     | `log_level`    | `info`               |
| `DB_PATH`       | `db_path`      | `./job-intel-bot.db` |
| `PROXY_URL`     | `proxy_url`    | —                    |

Пример запуска с env vars (без `config.yaml`):

```bash
BOT_TOKEN=123456789:AAFxxx POLL_INTERVAL=15m ./bot
```

### Прокси

Поддерживаются SOCKS5 и HTTP прокси — применяются только для Telegram API.
Habr Career и HeadHunter работают напрямую.

```yaml
# config.yaml
proxy_url: "socks5://user:pass@host:1080"
```

Или через env:

```bash
PROXY_URL=socks5://host:1080 ./bot
```

## Команды бота

| Команда                            | Описание                                                  |
|------------------------------------|-----------------------------------------------------------|
| `/start`                           | Приветствие и краткая справка                             |
| `/add <ключевое слово> [источник]` | Добавить подписку (`habr` или `hh`, по умолчанию `habr`) |
| `/list`                            | Список всех подписок                                      |
| `/edit`                            | Изменить фильтры подписки (выбор через inline-кнопки)     |
| `/remove`                          | Удалить подписку (выбор через inline-кнопки + подтверждение) |
| `/pause <id>`                      | Приостановить подписку                                    |
| `/resume <id>`                     | Возобновить подписку                                      |
| `/status`                          | Статистика подписок                                       |
| `/help`                            | Справка по командам                                       |

### Примеры

```
/add Go разработчик
/add Senior Backend hh
/add Python hh
/list
/pause 2
/resume 2
/edit
/remove
```

## Архитектура

```
cmd/bot/                  — точка входа: инициализация, планировщик, graceful shutdown
internal/
  config/                 — загрузка конфига (config.yaml + env vars)
  domain/                 — доменные типы: Job, Subscription, Filter, Source, Level
  repository/             — интерфейсы и реализации хранилища (JSON)
  parser/                 — HabrParser, HHParser, дедупликация по fingerprint
  service/                — NotificationService: fetch → diff → notify
  notifier/               — TelegramNotifier: форматирование и отправка сообщений
  scheduler/              — периодический опрос подписок
  bot/                    — обработчики Telegram-команд
```

**Поток данных:**
`Scheduler` → `NotificationService.CheckAndNotify` → `Parser.Parse` → diff по fingerprint → `TelegramNotifier.Notify`

## Разработка

```bash
go build -o bot ./cmd/bot  # сборка бинарника
go run ./cmd/bot            # запуск без сборки (для разработки)
go test ./...               # все тесты
go vet ./...                # статический анализ
```

## Лицензия

MIT
