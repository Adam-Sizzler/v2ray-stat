# v2ray-stat API

API для управления пользователями и статистикой сервера v2ray-stat.  
Все запросы по умолчанию отправляются на `http://127.0.0.1:9952`.

## Содержание

- [Обзор](#обзор)
- [Эндпоинты](#эндпоинты)
  - [Получение статистики по серверу и клиентам — GET /api/v1/stats](#получение-статистики-по-серверу-и-клиентам)
  - [Получение DNS-статистики — GET /api/v1/dns_stats](#получение-dns-статистики)
  - [Добавление пользователей на ноды — POST /api/v1/add_user](#добавление-пользователей-на-ноды)
  - [Удаление пользователей с нод — POST /api/v1/delete_user](#удаление-пользователей-с-нод)
  - [Обновление лимита IP — PATCH /api/v1/update_ip_limit](#обновление-лимита-ip)
  - [Сброс DNS-статистики — POST /api/v1/reset_dns_stats](#сброс-dns-статистики)
  - [Сброс статистики трафика — POST /api/v1/reset_bound_traffic](#сброс-статистики-трафика)
  - [Сброс статистики клиентов — POST /api/v1/reset_user_traffic](#сброс-статистики-клиентов)
  - [Включение/выключение пользователей — PATCH /api/v1/set_user_enabled](#включениевыключение-пользователей)
- [Примеры конфигурации и включение API в ядрах (Singbox, Xray)](#примеры-конфигурации-и-включение-api-в-ядрах)
- [Генерация сертификатов](#генерация-сертификатов)

## Обзор

Этот документ описывает API для сбора статистики и управления пользователями в проекте v2ray-stat. API позволяет:

- Получать статистику по серверам, клиентам и DNS-запросам с поддержкой фильтрации и сортировки.
- Добавлять и удалять пользователей на указанных нодах.
- Сбрасывать статистику DNS-запросов, трафика и клиентов.
- Обновлять лимит IP для пользователей.
- Включать/выключать пользователей на нодах.
- Настраивать интеграцию и включать API в ядрах (Singbox / Xray).

## Эндпоинты

### Получение статистики по серверу и клиентам

**GET /api/v1/stats**  
Возвращает статистику по серверу и клиентам. Поддерживает фильтрацию по нодам и пользователям, сортировку и агрегацию. Работа основана на конфигурации `stats_columns` и данных из таблиц `bound_traffic`, `user_traffic`, `user_data` и `user_uuids`.

**Пример запроса**  
```bash
curl -X GET "http://127.0.0.1:9952/api/v1/stats?node=node1,node2&user=user1,user2&sort_by=rate&sort_order=desc&aggregate=true"
```

#### Параметры запроса

| Параметр     | Описание                                              |
|--------------|-------------------------------------------------------|
| `node`       | Имя узла (или список через запятую) — фильтрация по нодам |
| `user`       | Имя пользователя (или список через запятую)            |
| `sort_by`    | Колонка для сортировки (см. поддерживаемые колонки)   |
| `sort_order` | `asc` или `desc`                                      |
| `aggregate`  | `true` — агрегировать данные по пользователям/источникам |

#### Поддерживаемые колонки для сортировки

**Server:**  
- `node_name` — имя ноды  
- `source` — источник трафика (IP или hostname)  
- `rate` — текущий трафик (бит/с)  
- `uplink` — всего отправлено (байт)  
- `downlink` — всего получено (байт)  
- `sess_uplink` — отправлено в текущей сессии (байт)  
- `sess_downlink` — получено в текущей сессии (байт)  

**Client:**  
- `node_name` — имя ноды  
- `user` — имя пользователя  
- `last_seen` — время последней активности  
- `rate` — текущий трафик (бит/с)  
- `uplink` — всего отправлено (байт)  
- `downlink` — всего получено (байт)  
- `sess_uplink` — отправлено в текущей сессии (байт)  
- `sess_downlink` — получено в текущей сессии (байт)  
- `created` — дата создания пользователя  
- `inbound_tag` — тег входящего соединения (например, `vless-in`)  
- `uuid` — уникальный идентификатор пользователя  
- `sub_end` — дата окончания подписки  
- `renew` — статус/период автопродления  
- `lim_ip` — ограничение по количеству IP  
- `ips` — список IP-адресов  
- `enabled` — статус активности пользователя  

#### Конфигурация примера (YAML)

```yaml
stats_columns:
  server:
    sort_by: rate
    sort_order: desc
    columns:
      - source
      - rate
      - uplink
      - downlink
  client:
    sort_by: user
    sort_order: asc
    columns:
      - user
      - last_seen
      - rate
      - uplink
      - downlink
      - inbound_tag
      - uuid
```

#### Пример без статистики по серверу

```yaml
stats_columns:
  server:
    columns: []
  client:
    columns:
      - user
      - rate
      - uplink
      - downlink
      - inbound_tag
      - uuid
```

#### Переопределение сортировки через URL

```bash
curl "http://127.0.0.1:9952/api/v1/stats?sort_by=uuid&sort_order=asc"
```

#### Пример ответа

```
➤ Server Statistics:
Node    Source         Rate    Uplink    Downlink
node1   192.168.1.1    500     1000      2000
node2   10.0.0.1       300     500       1500

➤ Client Statistics:
Node    User    Last seen         Rate    Uplink    Downlink    Inbound Tag    UUID
node1   user1   2025-08-08 12:00  200     300       400         vless-in       550e8400-e29b-41d4-a716-446655440000
node2   user2   2025-08-08 11:30  100     200       300         trojan-in      6ba7b810-9dad-11d1-80b4-00c04fd430c8
```

#### Примечания

- Если `columns` в конфигурации пустой — соответствующая часть статистики не отображается.  
- При агрегации (`aggregate=true`) данные группируются по пользователям (`user`) или источникам (`source`), а `node_name` исключается из результата.  
- Ответ форматируется как текстовая таблица с псевдонимами колонок (например, `Rate`, `Uplink`, `Inbound Tag`).  

### Получение DNS-статистики

**GET /api/v1/dns_stats**  
Возвращает статистику DNS-запросов из таблицы `user_dns`. Поддерживает фильтрацию по нодам, пользователям и доменам, а также ограничение количества записей.

**Пример запроса**  
```bash
curl -X GET "http://127.0.0.1:9952/api/v1/dns_stats?node=node1,node2&user=user1,user2&domain=example.com&count=20"
```

#### Параметры запроса

| Параметр | Описание                                                                 |
|----------|--------------------------------------------------------------------------|
| `node`   | Имя узла (или список через запятую) — фильтрация по нодам (опционально)   |
| `user`   | Имя пользователя (или список через запятую) — фильтрация по пользователям (опционально) |
| `domain` | Часть доменного имени для поиска (поддерживает подстроку, опционально)    |
| `count`  | Количество возвращаемых записей (по умолчанию 20, максимум 1000)         |

#### Пример ответа

```
➤ DNS Query Statistics:
Node    User    Count    Domain
node1   user1   150      example.com
node2   user1   100      sub.example.com
node1   user2   50       test.com
```

#### Примечания

- Если параметры `node`, `user` или `domain` не указаны, возвращаются все соответствующие записи.  
- Параметр `count` должен быть положительным числом (1–1000). Если не указан, используется значение по умолчанию (20).  
- Параметр `domain` поддерживает частичное совпадение (например, `example` найдет `example.com` и `sub.example.com`).  
- Ответ форматируется как текстовая таблица с колонками: `Node`, `User`, `Count`, `Domain`.

### Добавление пользователей на ноды

**POST /api/v1/add_user**  
Добавляет пользователей на указанные ноды. Если `nodes` не указан или пустой — пользователи добавляются на все доступные ноды. Пользователи добавляются в таблицу `user_traffic` и конфигурационные файлы нод (Xray/Singbox). После успешного добавления вызывается синхронизация пользователей с нодами через `users.SyncUsersWithNode`.

**Тело запроса (JSON)**  
- `users` — массив имён пользователей (строки, только буквы, цифры, дефис или подчеркивание)  
- `inbound_tag` — тег входящего соединения (строка), например `vless-in` или `trojan-in`  
- `nodes` — массив строк, список нод (опционально)  

**Пример запроса (один пользователь)**  
```bash
curl -X POST http://127.0.0.1:9952/api/v1/add_user -H "Content-Type: application/json" -d '{
    "users": ["testuser"],
    "inbound_tag": "vless-in",
    "nodes": ["node1", "node2"]
}'
```

**Пример запроса (несколько пользователей)**  
```bash
curl -X POST http://127.0.0.1:9952/api/v1/add_user -H "Content-Type: application/json" -d '{
    "users": ["testuser1", "testuser2"],
    "inbound_tag": "vless-in",
    "nodes": ["node1", "node2"]
}'
```

**Пример ответа**  
```json
{
  "message": "users added and synced successfully to all specified nodes",
  "usernames": ["testuser1", "testuser2"],
  "results": {
    "node1": "success",
    "node2": "success"
  }
}
```

**Пример ответа для Trojan**  
```json
{
  "message": "users added and synced successfully to all specified nodes",
  "usernames": ["testuser1", "testuser2"],
  "results": {
    "node1": "success",
    "node2": "success"
  }
}
```

#### Примечания

- Поля `users` и `inbound_tag` обязательны.  
- Для протокола `vless` возвращаются UUID в `results`, для `trojan` — сгенерированные пароли.  
- Если `nodes` не указан, пользователи добавляются на все ноды из конфигурации (`cfg.V2rayStat.Nodes`).  
- Ответ содержит JSON-объект, где ключи — имена нод, а значения — сгенерированные учетные данные (UUID для VLESS или пароль для Trojan).  
- Если используется `auth_lua`, учетные данные добавляются в `auth.lua`, а для Trojan применяется SHA224-хеширование пароля.  

### Удаление пользователей с нод

**POST /api/v1/delete_user**  
Удаляет пользователей с указанных нод. Если `nodes` не указан или пустой — пользователи удаляются со всех доступных нод. После успешного удаления вызывается синхронизация пользователей с нодами через `users.SyncUsersWithNode`.

**Тело запроса (JSON)**  
- `users` — массив имён пользователей (строки, только буквы, цифры, дефис или подчеркивание)  
- `inbound_tag` — тег входящего соединения (строка), например `vless-in` или `trojan-in`  
- `nodes` — массив строк, список нод (опционально)  

**Пример запроса (один пользователь)**  
```bash
curl -X POST http://127.0.0.1:9952/api/v1/delete_user -H "Content-Type: application/json" -d '{
    "users": ["testuser"],
    "inbound_tag": "vless-in",
    "nodes": ["node1", "node2"]
}'
```

**Пример запроса (несколько пользователей)**  
```bash
curl -X POST http://127.0.0.1:9952/api/v1/delete_user -H "Content-Type: application/json" -d '{
    "users": ["testuser1", "testuser2"],
    "inbound_tag": "vless-in",
    "nodes": ["node1", "node2"]
}'
```

**Пример ответа**  
```json
{
  "message": "users deleted and synced successfully from all specified nodes",
  "usernames": ["testuser1", "testuser2"],
  "results": {
    "node1": "success",
    "node2": "success"
  }
}
```

#### Примечания

- Поля `users` и `inbound_tag` обязательны.  
- Если `nodes` не указан, пользователи удаляются со всех нод из конфигурации (`cfg.V2rayStat.Nodes`).  
- Если используется `auth_lua`, пользователи удаляются из `auth.lua`.  

### Обновление лимита IP

**PATCH /api/v1/update_ip_limit**  
Обновляет лимит IP (поле `lim_ip`) для указанного пользователя в таблице `user_data`.

**Тело запроса (JSON)**  
- `user` — имя пользователя (обязательно, строка, только буквы, цифры, дефис или подчеркивание).  
- `lim_ip` — новый лимит IP (необязательно, число от 0 до 100, по умолчанию 0).  

**Пример запроса**  

Обновление лимита IP для пользователя:  
```bash
curl -X PATCH http://127.0.0.1:9952/api/v1/update_ip_limit -H "Content-Type: application/json" -d '{
    "user": "test",
    "lim_ip": 5
}'
```

Обновление лимита IP с установкой в 0 (если `lim_ip` не указано):  
```bash
curl -X PATCH http://127.0.0.1:9952/api/v1/update_ip_limit -H "Content-Type: application/json" -d '{
    "user": "test"
}'
```

**Пример ответа**  
```json
{
  "message": "IP limit updated successfully",
  "rows_affected": 1,
  "user": "test",
  "lim_ip": 5
}
```

#### Примечания

- Запрос использует метод PATCH и принимает параметры `user` и `lim_ip` в формате JSON (`application/json`).  
- Если `lim_ip` не указано в JSON (`null`), устанавливается значение 0.  
- Операция выполняется в рамках SQL-транзакции для обеспечения атомарности.  
- Ответ возвращается в формате JSON (`application/json; charset=utf-8`), включая сообщение об успехе, количество затронутых строк, имя пользователя и новое значение `lim_ip`.

### Сброс DNS-статистики

**POST /api/v1/reset_dns_stats**  
Сбрасывает статистику DNS-запросов в таблице `user_dns`. Поддерживает фильтрацию по нодам через параметр `nodes` в URL. Если `nodes` не указан, удаляются все записи.

**Параметры запроса (URL)**  
- `nodes` — список имён нод, разделённых запятыми (опционально). Например: `nodes=node1,node2`.

**Пример запроса**  

Сброс всех записей DNS-статистики:  
```bash
curl -X POST http://127.0.0.1:9952/api/v1/reset_dns_stats
```

Сброс записей для указанных нод:  
```bash
curl -X POST "http://127.0.0.1:9952/api/v1/reset_dns_stats?nodes=node1,node2"
```

**Пример ответа**  
```json
{
  "message": "DNS stats records deleted successfully",
  "rows_affected": 150
}
```

#### Примечания

- Запрос использует метод POST и принимает параметр `nodes` в URL.  
- Если параметр `nodes` не указан, удаляются все записи в таблице `user_dns`.  
- Если указаны `nodes`, удаляются только записи, где `node_name` соответствует указанным нодам.  
- Операция выполняется в рамках SQL-транзакции для обеспечения атомарности.  
- Ответ возвращается в формате JSON (`application/json; charset=utf-8`), включая сообщение об успехе и количество затронутых строк (`rows_affected`).

### Сброс статистики трафика

**POST /api/v1/reset_bound_traffic**  
Сбрасывает статистику трафика (поля `uplink` и `downlink`) в таблице `bound_traffic`. Поддерживает фильтрацию по нодам через параметр `nodes` в URL. Если `nodes` не указан, сбрасывается статистика для всех записей.

**Параметры запроса (URL)**  
- `nodes` — список имён нод, разделённых запятыми (опционально). Например: `nodes=swe,nl,rus`.

**Пример запроса**  

Сброс статистики трафика для всех нод:  
```bash
curl -X POST http://127.0.0.1:9952/api/v1/reset_bound_traffic
```

Сброс статистики трафика для указанных нод:  
```bash
curl -X POST "http://127.0.0.1:9952/api/v1/reset_bound_traffic?nodes=swe,nl"
```

**Пример ответа**  
```json
{
  "message": "Traffic stats reset successfully",
  "rows_affected": 15
}
```

#### Примечания

- Запрос использует метод POST и принимает параметр `nodes` в URL.  
- Если параметр `nodes` не указан, сбрасываются поля `uplink` и `downlink` для всех записей в таблице `bound_traffic`.  
- Если указаны `nodes`, сбрасываются поля `uplink` и `downlink` только для записей, где `node_name` соответствует указанным нодам.  
- Операция выполняется в рамках SQL-транзакции для обеспечения атомарности.  
- Ответ возвращается в формате JSON (`application/json; charset=utf-8`), включая сообщение об успехе и количество затронутых строк (`rows_affected`).

### Сброс статистики клиентов

**POST /api/v1/reset_user_traffic**  
Сбрасывает статистику трафика клиентов (поля `uplink` и `downlink`) в таблице `user_traffic`. Поддерживает фильтрацию по нодам через параметр `nodes` в URL. Если `nodes` не указан, сбрасывается статистика для всех записей.

**Параметры запроса (URL)**  
- `nodes` — список имён нод, разделённых запятыми (опционально). Например: `nodes=swe,nl,rus`.

**Пример запроса**  

Сброс статистики клиентов для всех нод:  
```bash
curl -X POST http://127.0.0.1:9952/api/v1/reset_user_traffic
```

Сброс статистики клиентов для указанных нод:  
```bash
curl -X POST "http://127.0.0.1:9952/api/v1/reset_user_traffic?nodes=swe,nl"
```

**Пример ответа**  
```json
{
  "message": "Client traffic stats reset successfully",
  "rows_affected": 39
}
```

#### Примечания

- Запрос использует метод POST и принимает параметр `nodes` в URL.  
- Если параметр `nodes` не указан, сбрасываются поля `uplink` и `downlink` для всех записей в таблице `user_traffic`.  
- Если указаны `nodes`, сбрасываются поля `uplink` и `downlink` только для записей, где `node_name` соответствует указанным нодам.  
- Операция выполняется в рамках SQL-транзакции для обеспечения атомарности.  
- Ответ возвращается в формате JSON (`application/json; charset=utf-8`), включая сообщение об успехе и количество затронутых строк (`rows_affected`).

### Включение/выключение пользователей

**PATCH /api/v1/set_user_enabled**  
Включает или выключает пользователей на указанных нодах. Если `nodes` не указан или пустой — изменения применяются ко всем доступным нодам. Операция обновляет статус в базе данных (`user_traffic`) и конфигурационных файлах нод через gRPC. После успешного обновления вызывается синхронизация пользователей с нодами через `users.SyncUsersWithNode`.

**Тело запроса (JSON)**  
- `users` — массив имён пользователей (обязательно, строки, только буквы, цифры, дефис или подчеркивание).  
- `enabled` — статус активности (булево: `true` для включения, `false` для выключения).  
- `inbound_tag` — тег входящего соединения (обязательно, строка, например `vless-in`).  
- `nodes` — массив строк, список нод (опционально).  

**Пример запроса (один пользователь)**  
```bash
curl -X PATCH http://127.0.0.1:9952/api/v1/set_user_enabled -H "Content-Type: application/json" -d '{
    "users": ["testuser"],
    "enabled": true,
    "inbound_tag": "vless-in",
    "nodes": ["node1", "node2"]
}'
```

**Пример запроса (несколько пользователей)**  
```bash
curl -X PATCH http://127.0.0.1:9952/api/v1/set_user_enabled -H "Content-Type: application/json" -d '{
    "users": ["testuser1", "testuser2"],
    "enabled": true,
    "inbound_tag": "vless-in",
    "nodes": ["node1", "node2"]
}'
```

**Пример ответа**  
```json
{
  "message": "users enabled status updated and synced successfully to all specified nodes",
  "usernames": ["testuser1", "testuser2"],
  "results": {
    "node1": "success",
    "node2": "success"
  }
}
```

#### Примечания

- Поля `users` и `inbound_tag` обязательны.  
- Операция выполняется параллельно для каждой ноды через gRPC с таймаутом 5 секунд.  
- Для ядра Xray или Singbox статус переключается путём перемещения конфигурации пользователя между основным файлом и файлом `.disabled_users`.  
- Обновление статуса в базе данных происходит в транзакции (поле `enabled` устанавливается в `"true"` или `"false"`).  

## Примеры конфигурации и включение API в ядрах

Ниже — примеры конфигурации для включения API и статистики в ядрах Singbox и Xray.

### Singbox (пример)

```json
{
  "experimental": {
    "v2ray_api": {
      "listen": "127.0.0.1:9953",
      "stats": {
        "enabled": true,
        "inbounds": [
          "trojan-in",
          "vless-in"
        ],
        "outbounds": [
          "warp",
          "direct",
          "IPv4"
        ],
        "users": [
          "user1",
          "user2"
        ]
      }
    }
  }
}
```

### Xray (пример)

```json
{
  "api": {
    "tag": "api",
    "listen": "127.0.0.1:9953",
    "services": [
      "HandlerService",
      "StatsService",
      "ReflectionService"
    ]
  }
}
```

## Генерация сертификатов

Примеры команд для проверки/просмотра сертификатов:  
```bash
openssl x509 -in /usr/local/etc/v2ray-stat/certs/node.crt -text -noout
openssl rsa -in /usr/local/etc/v2ray-stat/certs/node.key -check
```

### Рекомендации

- Используйте уникальные пары ключ/сертификат для каждой ноды или централизованно подпишите сертификаты CA.  
- Храните приватные ключи в защищённом каталоге с ограниченным доступом.

---

## Руководство по обновлению v2ray-stat компонентов

### Обновление Node

#### 1. Остановка сервиса
```bash
systemctl stop v2ray-stat-node.service
```

#### 2. Обновление через менеджер
```bash
v2ray-manager --update node
```

Или вручную:
```bash
/usr/local/bin/v2ray-manager --update node
```

#### 3. Запуск сервиса
```bash
systemctl start v2ray-stat-node.service
systemctl enable v2ray-stat-node.service
```

#### 4. Проверка статуса
```bash
systemctl status v2ray-stat-node.service
journalctl -u v2ray-stat-node.service -f
```

### Обновление Backend

#### 1. Остановка сервиса
```bash
systemctl stop v2ray-stat.service
```

#### 2. Обновление через менеджер
```bash
v2ray-manager --update backend
```

Или вручную:
```bash
/usr/local/bin/v2ray-manager --update backend
```

#### 3. Запуск сервиса
```bash
systemctl start v2ray-stat.service
systemctl enable v2ray-stat.service
```

#### 4. Проверка статуса
```bash
systemctl status v2ray-stat.service
journalctl -u v2ray-stat.service -f
```

### Обновление Manager

#### 1. Обновление менеджера
```bash
v2ray-manager --update manager
```

Или вручную:
```bash
/usr/local/bin/v2ray-manager --update manager
```

### Запуск менеджера после обновления

#### 1. Базовая команда:
```bash
v2ray-manager
```
