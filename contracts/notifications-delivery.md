# Notification delivery contract

For clients that show system notifications (the Mac app's main process, and the web notifier in `ui/src/attentionNotifier.ts`). Design: `docs/design/cloud-services.md` section 10. Server code: `internal/app/attention_delivery.go`. JSON uses snake_case.

The server decides what is shown. A client decides only three things that the device knows: whether the person looks at the conversation now, how to show an item, and where a click goes.

## 1. Find out if the server can deliver

`GET /api/server-info` (no sign-in needed) has this field when the server supports the delivery view:

```json
{"notifications": {"delivery": 1, "max_wait": 25}}
```

- `delivery` is the contract version. This file describes version `1`.
- `max_wait` is the longest `wait` in seconds.
- The field is absent on a server from before this change. Such a server knows the route but not `wait`: it answers at once, and a client in a loop would hammer it. **Send no delivery request when the field is absent.** Show: "The server needs an update to send notifications." Check again when the person connects a server.
- Both server modes (single workspace and accounts) send the field.

## 2. The request

`GET /api/notifications` with the session cookie of the signed-in account. Each account has its own database; no row crosses accounts.

| Query | Meaning |
|---|---|
| `view=delivery` | Return the delivery view (section 3). Without `view`, the answer is the unchanged phase 1 body: `{"notifications":[<pointer rows>],"next_after":N}`. |
| `after=<id>` | Cursor. Rows with a higher id are returned. Default `0`. Not a number, or negative: 400 `invalid_cursor`. |
| `limit=<n>` | Rows to scan. Default 50, cap 200. Below 1 or not a number: 400 `invalid_limit`. |
| `wait=<seconds>` | Long poll (section 4). Default `0`. Above `max_wait` is `max_wait`. Not a whole number or negative: 400 `invalid_wait`. |
| `tail=1` | Return no items and the current end of the table in `next_after` (section 5). Any other value: 400 `invalid_tail`. |
| `content=0` | Leave `preview` empty (section 3). `content=1` or no value: preview included. Other values: 400 `invalid_content`. |

An unknown `view` is 400 `invalid_view`. Other methods: 405 `method_not_allowed`. Every success has `Cache-Control: no-store`. 401 means the session ended: stop and wait for a new sign-in.

## 3. The delivery view

```
GET /api/notifications?view=delivery&after=41&wait=25
```

```json
{
  "items": [
    {
      "id": 47,
      "kind": "reply",
      "detail": "",
      "conversation_id": "9d1c...",
      "bot_id": "b2f7...",
      "message_seq": 12,
      "count": 3,
      "quiet": false,
      "title": "Research team",
      "bot_name": "Ada",
      "preview": "The report is ready. Two numbers changed since Friday.",
      "created_at": "2026-11-09T18:20:04.123456789Z"
    }
  ],
  "next_after": 47,
  "language": "en"
}
```

- `items` is always an array (empty is `[]`), oldest first.
- `next_after` is the id of the last row the server scanned. Rows that were filtered out also move it. Store it as the next `after`.
- `language` is the account's UI language (`""` means automatic). Use it for the fixed sentences below.

Item fields:

| Field | Meaning |
|---|---|
| `id` | Id of the last row merged into the item. A client that remembers the highest id it showed never shows an item twice. |
| `kind` | `reply`, `task_result`, `needs_you` or `spend_alert`. A client ignores a kind it does not know: it shows nothing and moves the cursor. |
| `detail` | `spend_warning` or `spend_cap` for `spend_alert`; `""` otherwise. |
| `conversation_id`, `bot_id` | Where it happened. Absent for a spend alert at the cap. |
| `message_seq` | The message to open, or `null` (`needs_you`, spend alert at the cap). |
| `count` | Rows merged into this item (same kind, detail and conversation in one response). `count > 1`: add "N new messages" to the line. |
| `quiet` | An earlier deliverable row of the same conversation is less than 30 seconds older. Replace the banner and make no sound. |
| `title` | The conversation name. Empty for a spend alert at the cap. |
| `bot_name` | The Bot of the last row. In a group, name it next to the preview. |
| `preview` | Text of the last message, up to 160 characters (light markdown and reply markers removed). Empty for `needs_you`, `spend_alert`, a message with no text, and `content=0`. |
| `created_at` | Server time, UTC, nanoseconds. A client can skip an item older than it wants to show (the Mac app uses 10 minutes). |

What the server leaves out before it builds `items`:

- A kind the account switched off: `notify_chats` (`reply`), `notify_tasks` (`task_result`), `notify_needs_you`, `notify_spend_alerts`.
- A row whose `message_seq` is at or below the conversation's read cursor (read on any device).
- A `needs_you` row when the conversation has no pending card on a waiting run (answered elsewhere).
- A row of a deleted conversation.

Fixed sentences are the client's, in the account language: `needs_you` is "waiting for your answer"; `spend_alert` with `spend_warning` is "80% of the monthly limit", with `spend_cap` "the monthly limit is reached"; an empty preview (and content off) is "new message". The server sends only `kind` and `detail` for these.

A click goes to `conversation_id` (with `message_seq` when set). A `spend_cap` item has no conversation: open Settings > Usage.

## 4. Long poll

With `wait=N` (N above 0) the server holds the request until a row with a higher id than `after` exists, then waits 2 more seconds so the rest of a burst lands in the same response and other devices can move their read cursor, then answers. If no row appears in N seconds it answers at once with empty `items` and `next_after` equal to `after`. If the client closes the connection the server stops waiting and writes nothing.

- The server checks once per second with a primary-key read.
- When a row exists, the answer takes at least the 2 second settle. An empty answer comes after the full `wait`. A client may count an empty answer that arrives in under 500 ms (with `wait` of 2 or more) as a failure and back off.
- One workspace holds at most 4 waiting requests. The fifth gets **429 `too_many_waiters`** at once. Treat it as a failure and back off. A request without `wait` is never counted.
- A browser does not long poll (six connections per origin). It sends `view=delivery` without `wait` on its existing 5 second beat.
- Set the client timeout to `wait` + 10 seconds (the design uses 35 for 25).
- The answer to a long poll has the same body as section 3.

## 5. Start a session: `tail=1`

A client that starts, signs in or changes server asks first:

```
GET /api/notifications?view=delivery&tail=1
```

```json
{"items": [], "next_after": 52, "language": ""}
```

It then reads from 52. Events from before are not shown. `next_after` is the highest id ever issued, not the highest id in the table, so a cursor taken after the 30-day cleanup emptied the table never points below a later row. The cursor lives in memory only. Do not keep a cursor across accounts.

Without `view`, `tail=1` gives `{"notifications":[],"next_after":N}`.

## 6. Errors

All errors are `{"error":{"code":"...","message":"..."}}`. A client decides by `code`.

| Status | Code | Client action |
|---|---|---|
| 400 | `invalid_cursor`, `invalid_limit`, `invalid_wait`, `invalid_view`, `invalid_content`, `invalid_tail` | A client bug. Do not retry the same request. |
| 401 | `auth_required` | Session ended. Stop. Resume after sign-in, from `tail=1`. |
| 405 | `method_not_allowed` | Use GET. |
| 429 | `too_many_waiters` | Back off. Another client of this account holds 4 waits. |
| 500 | `storage` | Back off. |

## 7. Account settings

`GET/PUT /api/preferences` has four notification switches, all default `true`: `notify_chats`, `notify_tasks`, `notify_needs_you`, `notify_spend_alerts`. The Bot settings tool and the settings registry use `account.notify_*`. Device-level switches (show on this Mac, show message content, open at login) live in the Mac app, not on the server.

## 8. The page-to-app bridge

A Mac app that notifies from its main process puts `window.tofiDesktop.notifications` in the page. The page uses only its existence to choose the outlet. With the bridge the page does not call `new Notification` and does not read the delivery view. Shape (`ui/src/desktop.ts`):

```ts
notifications: {
  status(): Promise<{ state: "on"|"off"|"not_connected"|"server_unsupported"|"signed_out"|"offline"|"blocked_by_system";
                      lastSuccessAt?: number; // epoch ms
                      settings: { enabled: boolean; content: boolean; openAtLogin: boolean } }>;
  update(patch: Partial<{ enabled: boolean; content: boolean; openAtLogin: boolean }>): Promise<status>;
  setView(view: { conversationId: string | null }): void;   // the conversation in view
  onStatus(listener: (status) => void): () => void;
}
```

- The page calls `setView` when the open conversation changes. The main process decides if the window is focused.
- The settings page shows the three switches and the state text only when the bridge exists.
- A click on a notification sends the command `{type: "open-conversation", id: string, seq?: number}` through `onCommand`. The page opens the conversation. It does not yet jump to `seq` (see AGENTS.md, Notifications).
