# RFC 007: Multi-turn A2A Task Support

Status: accepted by the maintainer on 2026-11-21 (audited 2026-11-20); not implemented yet  
Target: `internal/a2a`; docs and API description in `internal/server/openapi_schemas.go`, `internal/server/openapi_paths.go`, `docs/a2a.md`, `docs/api.md`, `README.md`, `CHANGELOG.md` (full list in section 8)  
Builds on: the partial A2A v1.0 HTTP+JSON binding in `internal/a2a` (`server.go`, `task_fsm.go`)  
Spec read: A2A specification at tag v1.0.1, `docs/specification.md` and `specification/a2a.proto` (the proto is the normative definition of the data objects, spec section 1.4). "Spec" and "proto" below mean these two files; line numbers refer to that tag.

## 1. Problem & Motivation

Gleann implements part of the A2A v1.0 HTTP+JSON binding in `internal/a2a`: the agent card, `message:send` and `GET tasks/{id}`. What is missing from v1.0 and not added here is listed in section 10. The task finite state machine in `internal/a2a/task_fsm.go` defines transitions for multi-turn execution:
- Line 49 permits `triggerRequireInput` from `TaskStateWorking` to `TaskStateInputRequired`.
- Lines 51–54 permit `triggerResume` from `TaskStateInputRequired` back to `TaskStateWorking`.
- Lines 83–87 (`triggerForA2ATarget`) specifically map target `TaskStateWorking` to `triggerResume` when the current state is `TaskStateInputRequired`.

However, the server runtime cannot execute multi-turn tasks:
1. **`INPUT_REQUIRED` is unreachable:** In `internal/a2a/server.go` (`handleSendMessage`, lines 267–291), handler evaluation is binary. If `err != nil`, the task transitions to `TaskStateFailed` (line 269). If `err == nil`, it transitions to `TaskStateCompleted` (line 281). Nothing ever transitions to `TaskStateInputRequired`.
2. **Every request spawns a new task:** `handleSendMessage` allocates a fresh task UUID unconditionally (`server.go`, lines 239–248). It never inspects incoming messages for an existing task ID.
3. **Missing task references in protocol payloads:** `Message` (`server.go`, lines 108–112) has no `taskId` or `contextId` field. In the spec both travel on the `Message` (proto lines 260–277), so `SendMessageRequest` needs neither. Because the JSON decoder ignores unknown fields (`server.go`, line 211), a client that sends `message.taskId` or `message.contextId` today gets a new task with new IDs and no error. For `contextId` the spec excludes exactly this: an agent keeps a client-provided `contextId` or rejects the request (spec 3.4.1, line 593).
4. **Resumed turns would be misrouted:** `handleSendMessage` calls `s.routeSkill` (`server.go`, line 230) on every request. A follow-up clarification (e.g., answering "use the second index") would be evaluated as a new query and routed by keyword matching instead of going to the skill awaiting input. This cannot happen while no task waits; it is a property the design must have.
5. **No TTL for waiting tasks:** `evictOldTasks` (`server.go`, lines 468–478) deletes only completed and failed tasks, and only when `len(s.tasks) > 1000`. A task waiting for input would never be removed and would stay in memory for the life of the process.

A concrete failure of today's single-turn behaviour that a question from the agent would fix: with several indexes and no `target`/`index`/`project` in the request metadata, `ask-rag` and `code-analysis` take the first accessible index without saying so (`internal/server/a2a_handler.go`, lines 126–136 and 259–269). The caller gets an answer from an index it did not choose.

What this RFC does not do by itself: no built-in skill asks for input today (all eight handlers in `internal/server/a2a_handler.go`, lines 37–44, return a result or a plain error; `docs/releases/v1.20.1.md`, lines 147–149, says so). Sections 2.1–2.9 specify the mechanism. Whether a built-in skill uses it in the same release is decision D2 (section 2.10). Without D2 the new states are reached only by test handlers.

## 2. Design

### 2.1 Task Identification on `message:send`
A client sends a `SendMessageRequest` to `POST /a2a/v1/message:send` (the only send endpoint; there is no `message:stream` today, and the agent card advertises `streaming: false`).

- **New Task:** When `message.taskId` is omitted, the server generates a new task ID, selects the skill via `routeSkill`, keeps the skill ID and the request metadata on the task (unexported fields, section 3), and begins execution.
- **Context of a new task:** If `message.contextId` is set, the new task takes that value unchanged. Gleann keeps no per-context state, so the value is only echoed; it is accepted up to 128 bytes, a longer one is rejected with HTTP 400 `INVALID_ARGUMENT`. If it is not set, the server generates one, as today. (Spec 3.4.3, line 626, allows `contextId` without `taskId`; 3.4.1, line 593, forbids replacing it.)
- **Resumed Task:** When `message.taskId` is set, the server looks up the task in `s.tasks`. As in the A2A spec, the task reference travels on the `Message`, not on the request envelope.
- If `message.contextId` is set on a resume, the server validates `contextId == task.ContextID`. If it is omitted, the task's value is used (spec 3.4.3, lines 627–628).
- The server skips `routeSkill` and re-invokes the handler registered for the task's skill. A resume request whose `metadata.skill` is set and differs from the task's skill is rejected with HTTP 400 `INVALID_ARGUMENT`.
- **Stored form of an incoming message.** The copy of the message kept in `History` is normalised so that the history never contains an invalid message (proto lines 245–262: `message_id` and `role` are required, the role enum is `ROLE_USER`/`ROLE_AGENT`):
  - a missing `messageId` is generated;
  - `role` is stored as `ROLE_USER` whatever the client sent (the documented examples send `"role": "user"` or nothing); a request whose role is `ROLE_AGENT` is rejected with HTTP 400 `INVALID_ARGUMENT`;
  - `taskId` and `contextId` are set from the task;
  - file parts are stored with `name` and `mimeType` only, without `bytes`. The handler of the current turn still receives the bytes through `ctx.Files`.
- **History length.** `configuration.historyLength` on `message:send` and `?historyLength=N` on `GET /a2a/v1/tasks/{id}` limit the history in the response: unset returns all of it, `0` omits the `history` field, `N > 0` returns the most recent `N` messages (spec 3.2.4; proto lines 150–154 and 667–671: "The server MUST NOT return more messages than the provided value"). A negative or non-numeric value is HTTP 400 `INVALID_ARGUMENT`. The stored history is not truncated. Before this RFC a task's history always had one message; with it the history grows by two per turn.
- Streaming (`message:stream`, SSE) is out of scope; a later RFC adds it on top of this lookup. A blocking `message:send` that returns at `INPUT_REQUIRED` is what the spec prescribes (3.2.2, line 446), so the multi-turn flow needs no stream.

### 2.2 Skill Input Requests (`InputRequiredError`)
Skills signal that input or clarification is required by returning `*InputRequiredError`:

```go
type InputRequiredError struct {
    Prompt string
}

func (e *InputRequiredError) Error() string { return e.Prompt }
```

The server detects it with `errors.As`, before the failure branch (`server.go`, line 267). A handler returns it directly or wrapped with `%w`; wrapped with `%v` it is a plain error and the task fails (the handlers in `internal/server/a2a_handler.go` wrap lower errors with `%v`, lines 144, 173, 181). A nil `*InputRequiredError` or a `Prompt` that is empty after trimming is a handler failure: the task becomes `FAILED` with `skill asked for input without a prompt`. A prompt longer than 4 KiB is cut at 4 KiB on a rune boundary. The string result returned together with the error is ignored.

When `handler(ctx)` returns `*InputRequiredError` and the limits of section 2.7 allow another round:
1. Server fires `TaskFSMTransition(task.fsm, TaskStateInputRequired)`.
2. Server replaces `task.Status` with `{State: TaskStateInputRequired, Timestamp: now, Message: prompt}`.
3. The prompt is `&Message{MessageID: <new UUID>, Role: "ROLE_AGENT", TaskID: task.ID, ContextID: task.ContextID, Parts: []Part{{Text: err.Prompt}}}`.
4. Server appends the same message to `task.History`. The prompt is in `History` as well as in `status.message` on purpose: the full exchange can be read from `History` after the task has moved on. Both copies have the same `messageId`, so a client that renders both can drop the duplicate.
5. Server saves the task and returns HTTP 200 with `SendMessageResponse{Task: <snapshot>}`.

Every message the server authors (input prompt, failure message, timeout message) carries `messageId`, `taskId` and `contextId` (proto lines 254–262: "For server messages, `context_id` must be provided, and `task_id` only if a task was created"). The existing failure message (`server.go`, lines 273–277) has only `messageId` today and gains the other two. Failure and timeout messages stay in `status.message` only, as today; only the input prompt is appended to `History`.

### 2.3 Resuming a Waiting Task
When the client replies to an `INPUT_REQUIRED` task:
1. Server appends the normalised copy of `req.Message` (2.1) to `task.History`.
2. Server fires `TaskFSMTransition(task.fsm, TaskStateWorking)`. The FSM executes `triggerResume`.
3. Server replaces `task.Status` with `{State: TaskStateWorking, Timestamp: now}`; the previous status message is not kept in `Status`. It refreshes the task's internal `updatedAt`.
4. Server constructs `SkillContext` as described in 2.3.1.
5. Server calls the handler of the task's skill.
6. If the handler returns a final answer, the task transitions to `TaskStateCompleted`. If another question is needed, it returns another `*InputRequiredError`. A plain error fails the task.

Steps 1–3 are one critical section (2.5).

#### 2.3.1 Handler contract
`SkillHandler` stays a plain function registered once (`server.go`, line 163); it holds no per-task state. On every turn it is called again from its start.

| `SkillContext` field | First turn | Resumed turn |
|---|---|---|
| `TaskID`, `ContextID` | the new task's IDs | the task's IDs |
| `Query` | text of the message | text of the current (follow-up) message |
| `History` | one entry, the request | a copy of the whole history in order, the current message last; `History[0]` is the original request |
| `Metadata` | the request metadata | the first turn's request metadata with the keys of each later request laid over it; the merged map is kept on the task |
| `Files` | file parts of the message | file parts of the current message only; attachments of earlier turns are not available (their bytes are not kept) |

Rules for a handler that asks for input:
- It returns `*InputRequiredError` before any side effect (a write, a model call). Whatever it did before asking is done again on the next turn.
- It derives what it needs from `ctx.History` and `ctx.Query`. `len(ctx.History) == 1` means first turn.
- It repeats every access check on every invocation and takes no authorisation decision from `History`. The handlers in `internal/server/a2a_handler.go` already check the index named in the metadata on each call (`isIndexAccessible`, lines 62, 123, 256), so a resume that names another index in its metadata goes through the same check.
- A prompt is built from fixed text and server-side values filtered by the access rule (`isIndexAccessibleMeta`). It does not contain model output or passage text: a prompt is an instruction to the calling agent, and indexed text must not be able to write one.

Why the metadata is merged and not taken from the current request only: every handler reads its index and model from `ctx.Metadata` (`a2a_handler.go`, lines 52–58, 113–120, 150–158). Turn 1 with `{"index":"docs","llm_model":"m2"}` and a turn 2 without metadata would otherwise run against the first accessible index and the default model.

### 2.4 Error Handling & Status Codes
When a request targets an invalid or inappropriate task, the server responds with the A2A error envelope of the HTTP+JSON binding (spec 11.6): `error.code` is the HTTP status, `error.status` the google.rpc code name, `error.message` a human-readable string, and for A2A-specific errors `error.details` holds a `google.rpc.ErrorInfo` with the A2A error name in `reason` ("implementations MUST include a `google.rpc.ErrorInfo` object in the `details` array for A2A-specific errors", spec line 2910).

| # | Condition | HTTP | `error.status` | `details[0].reason` | `details[0].metadata` | Message |
|---|---|---|---|---|---|---|
| 1 | `taskId` not found in `s.tasks` (never existed, deleted by expiry or eviction, or the server restarted) | 404 | `NOT_FOUND` | `TASK_NOT_FOUND` | `taskId` | `task "<id>" not found` |
| 2 | Task is in a terminal state (`COMPLETED`, `FAILED`; `CANCELED` cannot be produced today, section 10) | 400 | `FAILED_PRECONDITION` | `UNSUPPORTED_OPERATION` | `taskId`, `state` | `task "<id>" is in terminal state <state>` |
| 3 | Task is not waiting and not terminal: `WORKING` (including the loser of a race, see 2.5) | 400 | `FAILED_PRECONDITION` | `UNSUPPORTED_OPERATION` | `taskId`, `state` | `task "<id>" is not waiting for input (state <state>); poll GET /a2a/v1/tasks/<id>` |
| 4 | `contextId` does not match `task.ContextID` | 400 | `INVALID_ARGUMENT` | none | | `contextId mismatch for task "<id>"` |
| 5 | Invalid request: no text or file part, role `ROLE_AGENT`, `contextId` over 128 bytes, follow-up text over 64 KiB, bad `historyLength`, `metadata.skill` differs from the task's skill | 400 | `INVALID_ARGUMENT` | none | | names the field |

Example body for row 2:

```json
{"error": {"code": 400, "status": "FAILED_PRECONDITION",
  "message": "task \"…\" is in terminal state TASK_STATE_COMPLETED",
  "details": [{"@type": "type.googleapis.com/google.rpc.ErrorInfo",
    "reason": "UNSUPPORTED_OPERATION", "domain": "a2a-protocol.org",
    "metadata": {"taskId": "…", "state": "TASK_STATE_COMPLETED"}}]}}
```

Checked against the spec (this closes the draft's open point):
- Row 1: `TaskNotFoundError`, HTTP 404 (spec 3.4.2, line 614, a MUST; mapping table 5.4, line 1182).
- Row 2: `UnsupportedOperationError`, HTTP 400, gRPC `FAILED_PRECONDITION` (spec 3.1.1, line 175; 5.4, line 1185).
- Row 3: the spec names no error for a non-terminal task that does not accept a message; it only says agents MAY accept such messages (3.3.3, line 567). The same error as row 2 is used, and `metadata.state` lets a client tell "poll and you will get the result" from "start a new task".
- Row 4: a MUST reject (3.4.3, line 628), no error type named; `INVALID_ARGUMENT` without an A2A reason.
- The HTTP statuses of the draft (404, 400, 400, 400) were right. The body was not: `writeA2AError` (`server.go`, lines 480–494) puts the A2A name into `error.status` and sends no `details`; `ErrorResponse` (lines 151–157) has no `details` field.

The body layout applies to every A2A error, so three existing responses change with it (decision D1): `TASK_NOT_FOUND` on `GET tasks/{id}` becomes status `NOT_FOUND` with the reason in `details` (`server.go`, line 314); `UNSUPPORTED_OPERATION` for an unknown skill becomes `FAILED_PRECONDITION` with the reason in `details` (line 233); `INVALID_ARGUMENT` (lines 212, 225) and `INTERNAL` (line 252) keep their status and have no A2A reason. If D1 is answered "additive only", `error.status` keeps today's values and only `details` is added; nothing else in this RFC depends on it. One caveat on the evidence: the normative bullets of 11.6 name `code`, `message` and `details`; the google.rpc name in `status` appears in the section's example body only.

Order of checks for a message with `taskId`: (a) the request decodes and passes the row 5 checks that need no task (task untouched); (b) the task exists after the expiry check of 2.6 (row 1); (c) `contextId` (row 4) and `metadata.skill` (row 5); (d) replay (2.5); (e) state (rows 2 and 3).

Responses produced before the A2A handler are not A2A envelopes: 401 and 403 from the security middleware, 429 from the rate limiter and 504 from the timeout middleware have gleann's general shape `{"error":"..."}` (`internal/server/server.go`, line 1588). After a 504 or a dropped connection on a follow-up, the client reads the outcome with `GET /a2a/v1/tasks/{id}` or re-sends the same message with the same `messageId` (2.5).

### 2.5 Concurrency
Two follow-ups may race on the same task. The check and the claim are one step under the server lock; no lock is held while the handler runs:
1. Under `s.mu.Lock()`: look up the task and apply the expiry check (2.6), validate `contextId` and `metadata.skill`, check for a replay (below), require `State == TaskStateInputRequired`, fire `TaskFSMTransition(task.fsm, TaskStateWorking)`, replace the status, append the message to `History`, merge the metadata, and copy the history for the handler. Unlock.
2. The second request of a race finds `WORKING` and gets row 3 of 2.4. Its message is not appended and the handler is not called for it.
3. Run the handler without any lock, inside a function with a deferred `recover`. A panic is logged and becomes an ordinary failure with the fixed message `internal error`; the panic value is not sent to the client. The same function runs the first turn of a new task.
4. Under `s.mu.Lock()` again: apply the outcome (completed, failed, or input-required) only if the task is still `WORKING`, and refresh `updatedAt`. If the task was moved meanwhile (the `WORKING` age limit of 2.6) or `TaskFSMTransition` returns an error, the status is left as it is and the outcome is discarded. A terminal task never returns to `WORKING`, so "still `WORKING`" identifies the turn. Take the response snapshot in the same critical section.

A new task is stored at step 4, after its first handler call returned, as today (`server.go`, lines 293–300); only a resumed task is ever `WORKING` inside the store.

**Replay of a follow-up.** `/a2a/v1/message:send` has the default request timeout of 60 s (`internal/server/timeout.go`, lines 42 and 83–96), while the handler is not cancelled when the deadline passes: `SkillHandler` takes no context and the skills call the engines with `context.Background()` (`a2a_handler.go`, lines 84, 142, 171, 179). A client that got a 504 and re-sends its follow-up is an ordinary sequence. Without a rule the re-sent message is rejected although the first attempt was applied, or, if the handler asked a second question meanwhile, it is taken as the answer to that question. The rule (spec 3.3.1, line 495: agents may use the `messageId` to detect duplicates):
- A follow-up whose `messageId` is non-empty in the request and equals the `messageId` of a `ROLE_USER` message already in the task's `History` is a replay. Nothing is appended and the handler is not invoked.
- If the task is `WORKING`, the answer is row 3 of 2.4 (the client polls `GET`). Otherwise the answer is HTTP 200 with the current snapshot of the task, whatever its state.
- A request without `messageId` is never a replay. A re-sent first message (no `taskId`) creates a second task, as today.

A task is driven by one client at a time. A message that is not a replay is matched to whatever question is open when it arrives; the A2A `Message` has no in-reply-to field, so the server does not check which question a message answers.

**Readers.** The draft said `GET /tasks/{id}` already runs under `s.mu`. Only the map lookup does: `handleGetTask` releases the lock at `server.go` line 311 and encodes the live `*Task` at line 319; `handleSendMessage` unlocks at line 300 and encodes at line 303. That is safe today because a stored task never changes; with resume it is a data race. Both handlers therefore encode a snapshot taken under `s.mu`: `func (t *Task) snapshot(historyLength *int) Task` copies the struct, the `History` and `Artifacts` slices and the `Status.Message` pointer target, and leaves `fsm` nil. `handleGetTask` takes the write lock, because the lookup may expire the task. No code reads a stored `*Task` outside `s.mu`; `History` entries are never modified in place. `Task` gets no mutex of its own (it is copied for JSON responses, and a copied mutex is a `go vet` error).

### 2.6 Task Eviction and TTL Interplay
Multi-turn tasks introduce stateful waiting periods. The durations are constants in `internal/a2a` for the experimental phase; there is no new config key and no environment variable.

1. **Waiting TTL:** A task in `TaskStateInputRequired` expires 15 minutes after it entered that state. A resume starts a new 15 minutes when the handler asks again.
2. **Working age limit:** A stored task that has been `WORKING` for more than 30 minutes (a handler that hangs; 30 minutes is the floor of the HTTP write timeout, `timeout.go`, lines 62–68) is failed with `handler did not finish`.
3. **Terminal TTL:** Tasks in a terminal state are retained for retrieval up to 1 hour after their last state change, then deleted.

**Expiry is checked where a task is looked up**, under the lock, in the resume path (2.5 step 1) and in `GET /tasks/{id}`, before any other check:
- an `INPUT_REQUIRED` task past its TTL is moved to `FAILED` with the status message `task timed out waiting for input`;
- a `WORKING` task past the age limit is moved to `FAILED`;
- in both cases the status timestamp and `updatedAt` are set to the instant the limit was reached, not to the time of the lookup;
- a terminal task past its TTL is deleted.

The request then sees the result. For a task that entered `INPUT_REQUIRED` at t0 and got no reply, the answer to a follow-up depends on the clock only: before t0+15 min it resumes; from t0+15 min it gets row 2 of 2.4 (terminal state `FAILED`), and `GET` shows `TASK_STATE_FAILED` with the timeout message; from t0+75 min both get 404. Within the middle window the answer can already be 404 if the store passed 1000 tasks and the sweep deleted the failed task.

There is no background goroutine: `a2a.Server` has no `Close`, `mountA2A` drops the value after `Mount` (`internal/server/a2a_handler.go`, lines 34–46), and `buildMux` is called from 12 test sites that nothing would stop. `Server` gets an unexported `now func() time.Time` (default `time.Now`) that tests replace.

**Sweep.** When `len(s.tasks) > 1000` after a new task is stored, as today (`server.go`, line 297):
- the expiry rules above are applied to every task;
- expired terminal tasks are thereby deleted first;
- the oldest terminal tasks (by `updatedAt`) are deleted next until `len(s.tasks) <= 500`;
- `WORKING` tasks and `INPUT_REQUIRED` tasks within their limits are never evicted. Their number is bounded by 2.7.

The sweep sets a status directly when a task has no `fsm` (the tasks built in `internal/a2a/evict_cov_test.go`, lines 15–20 and 49–54, have none and have zero times; `TaskFSMTransition` on a nil machine would dereference nil at `task_fsm.go`, line 66).

### 2.7 Limits
Section 2.6 of the draft made waiting tasks non-evictable without bounding them. With the default rate limit of 60 requests/s per address (`internal/server/ratelimit.go`, lines 48–49) one address could hold 60 × 900 = 54,000 waiting tasks inside one 15-minute window, each keeping its request message whole (body limit 16 MiB, `internal/server/bodylimit.go`, line 18). The precondition is a skill that asks for input on text the caller controls, which is what a multi-turn skill does.

| Limit | Value | When reached |
|---|---|---|
| Tasks waiting for input at once | 100 | a task that would enter `INPUT_REQUIRED` is set to `FAILED` with `too many tasks waiting for input`; waiting tasks are not touched |
| Input requests per task | 8 | the ninth `InputRequiredError` fails the task with `too many input requests` |
| Text of a follow-up message | 64 KiB | HTTP 400 `INVALID_ARGUMENT`; the task stays `INPUT_REQUIRED` |
| Prompt | 4 KiB | cut (2.2) |
| Client `contextId` | 128 bytes | HTTP 400 `INVALID_ARGUMENT` |
| File bytes in `History` | none kept | 2.1 |

The values are proposals and are not measured (decision D6). They are far above a clarification dialogue and far below the body limit. The count of waiting tasks is taken by scanning the store under the lock at the moment a task is about to wait; the store holds about 1000 tasks at most.

Resulting bounds: a task waits at most 8 × 15 min in total. What multi-turn adds to a task's history is at most 8 × 64 KiB of follow-up text plus 8 × 4 KiB of prompts, 544 KiB; for 100 waiting tasks about 53 MiB, and about 531 MiB if all 1000 retained tasks went through eight full rounds. The first message of a task is stored whole as today, up to the body limit; that exposure exists today (1000 retained tasks) and shrinks, because file bytes are no longer kept. The number of `WORKING` tasks in the store equals the number of resumed handlers still running.

### 2.8 Where tasks live and who may use them
Tasks are kept in process memory (`s.tasks`, `server.go`, line 176; one `a2a.Server` per mux, `a2a_handler.go`, line 34). A restart of `gleann serve` drops all of them; a follow-up then gets 404 `TASK_NOT_FOUND` and the client starts a new task. Persisting tasks is a non-goal: the SQLite store is one database per index and tasks belong to no index. A handler that follows 2.3.1 has no side effect before it asks, so a lost waiting task loses no committed work.

Access model (decision D3): tasks are not bound to the token that created them. Every `/a2a/` request needs the `query` scope (`internal/server/security.go`, lines 81–82), and any token with that scope (or any local process when auth is off) that presents the task ID can read the task and, with this RFC, answer a waiting task. The ID is a random UUID and works as a handle. A foreign follow-up does not widen index access, because index access is a server-wide rule and not per token (`internal/server/server.go`, `isIndexAccessibleMeta`); what it reaches is the task's history. This is gleann's authorisation model for A2A tasks in the sense of spec 13.1 ("authorization models are agent-defined"; agents SHOULD document them) and is stated in `docs/a2a.md`. Binding a task to its creator needs the token identity in the request context, which `securityMiddleware` does not pass on today (`security.go`, lines 171–186; `auth.Store.Verify` returns only the scope).

### 2.9 Not changed
- The agent card. A2A v1.0 has no capability flag for multi-turn tasks (`AgentCapabilities`, proto lines 411–420).
- The routes. No route is added; `docs/api.md` and the route check in `internal/doccheck` are unaffected.
- The server never produces `TASK_STATE_AUTH_REQUIRED` or `TASK_STATE_REJECTED`; the spec does not require an agent to use them.
- `SkillHandler`'s signature. The eight handlers compile unchanged; every `SkillContext` literal in the tree uses field names.
- The request timeout of `/a2a/v1/message:send` (60 s default; decision D4).

### 2.10 First user (optional, decision D2)
If the maintainer agrees, `ask-rag` becomes the first skill that asks:
- Condition: the metadata has no `target`/`index`/`project`, the message has no file part, and more than one index is accessible. With a file part the skill behaves as today, because an attachment is not carried to the next turn (2.3.1).
- Prompt: fixed text plus the names of the accessible indexes (`isIndexAccessibleMeta`).
- Reply: the text of the follow-up, trimmed, compared case-insensitively with the accessible index names at that moment. A match runs the question from `History[0]` on that index. No match asks again, which counts toward the limit of 8.
- The check and the question come before the searcher is acquired and before the model call (2.3.1).

This changes a single-turn answer: a client with two or more accessible indexes that sends no index in the metadata gets `TASK_STATE_INPUT_REQUIRED` where it used to get an answer from the first index. A client that sends `metadata.index` sees no change.

## 3. Data Structures & Interfaces Changed

New wire fields: `message.taskId`, `message.contextId`, `configuration.historyLength`, `error.details`. New handler fields: `SkillContext.TaskID`, `SkillContext.ContextID`, `SkillContext.History`. New type `InputRequiredError`. The JSON shape of `Task` does not change: the skill, the merged metadata, the update time and the round counter are unexported. The time of the last state change stays on the wire as `status.timestamp`. (The draft had `SkillID` and `UpdatedAt` as JSON fields; the A2A `Task` object has neither, proto lines 167–184, and `omitempty` does not omit a zero `time.Time`.)

```go
// internal/a2a/server.go

type Message struct {
    MessageID string `json:"messageId"`
    Role      string `json:"role"`
    Parts     []Part `json:"parts"`
    TaskID    string `json:"taskId,omitempty"`
    ContextID string `json:"contextId,omitempty"`
}

type SendMessageConfiguration struct {
    HistoryLength *int `json:"historyLength,omitempty"`
}

type SendMessageRequest struct {
    Message       Message                   `json:"message"`
    Configuration *SendMessageConfiguration `json:"configuration,omitempty"`
    Metadata      map[string]interface{}    `json:"metadata,omitempty"`
}

type SkillContext struct {
    TaskID    string
    ContextID string
    Query     string
    Metadata  map[string]interface{}
    Files     []FilePart
    History   []Message
}

type Task struct {
    ID        string     `json:"id"`
    ContextID string     `json:"contextId,omitempty"`
    Status    TaskStatus `json:"status"`
    Artifacts []Artifact `json:"artifacts,omitempty"`
    History   []Message  `json:"history,omitempty"`

    fsm         *stateless.StateMachine
    skillID     string
    metadata    map[string]interface{} // first turn's, later turns laid over it
    updatedAt   time.Time              // last state change; drives 2.6
    inputRounds int                    // times the task entered INPUT_REQUIRED
}

type InputRequiredError struct {
    Prompt string
}

func (e *InputRequiredError) Error() string { return e.Prompt }

type ErrorResponse struct {
    Error ErrorBody `json:"error"`
}

type ErrorBody struct {
    Code    int           `json:"code"`
    Status  string        `json:"status"`
    Message string        `json:"message"`
    Details []ErrorDetail `json:"details,omitempty"`
}

// ErrorDetail is a google.rpc.ErrorInfo.
type ErrorDetail struct {
    Type     string            `json:"@type"`
    Reason   string            `json:"reason"`
    Domain   string            `json:"domain"`
    Metadata map[string]string `json:"metadata,omitempty"`
}

type Server struct {
    // existing fields
    now func() time.Time // time.Now; replaced in tests
}
```

`ErrorResponse.Error` becomes a named struct; `e.Error.Status` in the existing tests still compiles.

## 4. Build Tag & Platform Impact

None. The implementation is 100% pure Go (`CGO_ENABLED=0`). It uses the standard library and the existing FSM dependency. No CGo, Tree-sitter, FAISS, or external daemon dependencies are introduced; `internal/a2a` and `internal/server/a2a*.go` have no build constraints. Multi-platform compatibility across Linux, macOS, and Windows (`amd64` and `arm64`) is preserved.

Configuration surface: no new settings. The durations of 2.6 and the limits of 2.7 are constants, so `pkg/gleann/types.go`, `docs/configuration.md`, `docs/env-vars.md` and the settings registry are not touched.

## 5. Incremental & Migration Strategy

Nothing is persisted: no index format, no database table and no config key changes, so there is no data migration and a revert of any step leaves no state behind. Tasks do not survive a restart, before or after (2.8). The steps of section 7 are one commit each and each passes the gate on its own.

Unchanged:
- **Single-turn requests:** Clients omitting `taskId` follow the existing code path: a new task is created and runs directly to completion or failure.
- **Existing skill handlers:** Handlers returning `(string, nil)` or standard errors continue to complete or fail in one turn without modification.
- **Task inspection:** `GET /a2a/v1/tasks/{id}` continues to serialize `Task` with the same keys (`id`, `contextId`, `status`, `artifacts`, `history`). Inspecting a waiting task reports `TASK_STATE_INPUT_REQUIRED`.

Changed, visible to clients (each goes under "Changed" in `CHANGELOG.md`):
1. A message carrying `message.taskId` used to create a new task, because the field was ignored. It now resumes that task or fails with 404/400. A client that wants a new task related to a finished one sends `contextId` only.
2. A client-supplied `message.contextId` on a new task is kept; it used to be replaced by a generated one.
3. Error bodies follow spec 11.6 (2.4, decision D1): `error.status` changes for two existing errors, and A2A-specific errors gain `details`.
4. Messages in `history` and `status.message` carry `taskId` and `contextId`; a missing `messageId` is generated; the role of a stored client message is `ROLE_USER`; file parts in `history` have no `bytes`. A request with role `ROLE_AGENT` is rejected.
5. `historyLength` is honoured on `message:send` and on `GET tasks/{id}`.
6. Finished tasks are deleted 1 hour after their last state change. Before, they stayed until the store passed 1000 tasks. `docs/a2a.md`, line 117, promises later retrieval and gets the limit added.
7. A panic in a skill handler becomes a `FAILED` task. Before, by reading the code and not by running it, it ended the process: `timeoutMiddleware` runs the handler in its own goroutine (`timeout.go`, lines 120–123) and there is no `recover()` in the non-test code of `internal/server`, `internal/a2a` or `cmd/gleann`.
8. Only with D2: `ask-rag` may answer with `TASK_STATE_INPUT_REQUIRED` (2.10).

Tests:
- `internal/a2a/server_test.go`: no assertion changes by reading (`TestGetTask_NotFound` checks the HTTP code only).
- `internal/a2a/server_h2_test.go`: line 76 expects `UNSUPPORTED_OPERATION` in `error.status` and changes to `FAILED_PRECONDITION` plus the reason in `details` (with D1 "additive only" it stays). Line 37 (`INVALID_ARGUMENT`) stays.
- `internal/a2a/evict_cov_test.go`: calls `s.evictOldTasks()` directly on tasks without `fsm` and with zero times; the sweep tolerates both (2.6) and the file is re-checked in step 5.
- New tests that place a task in the store build it with `NewTaskFSM`.

## 6. Test & Verification Matrix

Protocol rows are tests in `internal/a2a` with stub handlers (new file `multiturn_test.go`; error body rows in `server_h2_test.go`). Rows of group I are in `internal/server/a2a_skills_test.go`. Rows that mention time use the injected clock. Groups C to G run with `-race`. "Unchanged" in an error row means: the task's state and `History` are the same after the request.

**A. Single turn**

| # | Case | Request / setup | Expected |
|---|---|---|---|
| A1 | Success | `message:send` (no `taskId`) | `SUBMITTED` → `WORKING` → `COMPLETED`; HTTP 200, artifact; the task's key set is `id`, `contextId`, `status`, `artifacts`, `history` |
| A2 | Failure | handler returns an error | `FAILED`; HTTP 200; `status.message` has `messageId`, `taskId`, `contextId`, role `ROLE_AGENT` |
| A3 | Client `contextId` on a new task | `contextId: "ctx-1"`, no `taskId` | response `task.contextId == "ctx-1"` |
| A4 | Over-long `contextId` | 200 bytes | HTTP 400 `INVALID_ARGUMENT` |
| A5 | Lenient message | no `messageId`, `"role": "user"` | HTTP 200; `history[0]` has a `messageId`, role `ROLE_USER`, `taskId`, `contextId` |
| A6 | Agent role from a client | `"role": "ROLE_AGENT"` | HTTP 400 `INVALID_ARGUMENT` |
| A7 | File part | one file with bytes | handler gets the bytes in `ctx.Files`; the `history` JSON has `name` and `mimeType`, no `bytes` |
| A8 | Handler panics | first turn | HTTP 200, `FAILED`, message `internal error`; the test process survives |
| A9 | Context of the first turn | any | `ctx.TaskID`/`ctx.ContextID` equal the response; `len(ctx.History) == 1` |

**B. Input request**

| # | Case | Request / setup | Expected |
|---|---|---|---|
| B1 | Skill requests input | handler returns `InputRequiredError` | `SUBMITTED` → `WORKING` → `INPUT_REQUIRED`; HTTP 200; `status.message` is the prompt with role `ROLE_AGENT`, `messageId`, `taskId`, `contextId`; `history` is [user-1, prompt]; both prompt copies have the same `messageId`; no artifact |
| B2 | Wrapped error | `fmt.Errorf("...: %w", inputErr)` | `INPUT_REQUIRED` |
| B3 | No prompt | empty `Prompt`; nil `*InputRequiredError` | `FAILED`, `skill asked for input without a prompt` |
| B4 | Long prompt | 5 KiB | prompt in the response is 4 KiB |
| B5 | Get while waiting | `GET tasks/{id}` | `TASK_STATE_INPUT_REQUIRED` and the prompt |

**C. Resume**

| # | Case | Request / setup | Expected |
|---|---|---|---|
| C1 | Resume waiting task | `message:send` with valid `taskId` | `INPUT_REQUIRED` → `WORKING` → `COMPLETED`; HTTP 200; `history` is [user-1, prompt, user-2]; `status.message` absent; artifact present |
| C2 | What the handler receives | as C1 | `Query` is the follow-up text; `History` has three entries, the current message last; `TaskID`/`ContextID` are the task's; `Files` holds the current message's files only |
| C3 | Metadata | turn 1 `{"index":"docs","llm_model":"m2"}`; turn 2 none, then turn 2 `{"index":"other"}` | handler sees both keys; then `index: other` and `llm_model: m2` |
| C4 | Routing on resume | two skills registered; follow-up text contains the other skill's keyword | the task's skill is called; the other handler's counter is 0 |
| C5 | Conflicting `metadata.skill` | resume names another skill | HTTP 400 `INVALID_ARGUMENT`; unchanged |
| C6 | Multi-turn continuation | resumed handler returns another `InputRequiredError` | `INPUT_REQUIRED` → `WORKING` → `INPUT_REQUIRED`; HTTP 200, next prompt; `history` has four entries |
| C7 | Resume ends in failure | handler returns a plain error on turn 2 | `FAILED`, HTTP 200 |
| C8 | `contextId` on resume | matching value; omitted | HTTP 200 both; response `contextId` is the task's |
| C9 | Handler panics on resume | | `FAILED`, `internal error`; `GET` shows `FAILED` |
| C10 | No repeated side effect | handler asks before its side effect (counter) | counter is 1 after the resumed turn |

**D. Errors** (each row asserts the HTTP code, `error.status` and, where the table of 2.4 has one, `details[0].reason` and `metadata`)

| # | Case | Request / setup | Expected |
|---|---|---|---|
| D1 | Unknown task ID | `message:send` with unknown `taskId` | HTTP 404, `NOT_FOUND` / `TASK_NOT_FOUND` |
| D2 | Resume terminal task | `COMPLETED`; `FAILED` | HTTP 400, `FAILED_PRECONDITION` / `UNSUPPORTED_OPERATION`, `metadata.state`; unchanged |
| D3 | Resume running task | the state is produced by a resumed handler blocked on a channel (in step 3, before resume exists, by a task placed in the store as `WORKING`) | HTTP 400, same error, `metadata.state` `TASK_STATE_WORKING`; unchanged |
| D4 | Context ID mismatch | `INPUT_REQUIRED`, other `contextId` | HTTP 400 `INVALID_ARGUMENT`, no `details`; unchanged |
| D5 | Empty follow-up | no text and no file | HTTP 400 `INVALID_ARGUMENT`; the task stays `INPUT_REQUIRED` |
| D6 | Over-long follow-up | 65 KiB text | HTTP 400 `INVALID_ARGUMENT`; the task stays `INPUT_REQUIRED` |
| D7 | Restart | waiting task on one `Server`; follow-up to a new `Server` | HTTP 404, `TASK_NOT_FOUND` |
| D8 | Existing errors | malformed JSON; no handler for the skill; `GET` of an unknown ID | `INVALID_ARGUMENT`; `FAILED_PRECONDITION` / `UNSUPPORTED_OPERATION`; `NOT_FOUND` / `TASK_NOT_FOUND` |

**E. Concurrency and replay**

| # | Case | Request / setup | Expected |
|---|---|---|---|
| E1 | Concurrent follow-ups | two different messages race on one `taskId`; the handler blocks on a channel until both requests have passed the claim | one HTTP 200; one HTTP 400 as D3; the handler ran once; the winner's message is in `history` once |
| E2 | Second message after the next question | handler asks again; a different follow-up arrives after the first returned | accepted as the next turn (2.5: one client at a time) |
| E3 | Readers | 8 goroutines poll `GET tasks/{id}` across two resumed turns | race detector clean; `GET` during a blocked handler shows `TASK_STATE_WORKING` |
| E4 | Replay after the next question | follow-up m2; handler asks Q2; m2 again with the same `messageId` | HTTP 200, `INPUT_REQUIRED` with Q2; handler counter unchanged; m2 is in `history` once |
| E5 | Replay after completion | m2 again after `COMPLETED` | HTTP 200 with the artifact; counter unchanged |
| E6 | Replay while running | m2 again while the handler is blocked | HTTP 400 as D3; counter is 1 |
| E7 | No `messageId` | the same follow-up twice without `messageId` | the second is judged by state, not as a replay |
| E8 | Late outcome | handler blocked; clock +31 min; `GET`; then release the handler | `GET` shows `FAILED`, `handler did not finish`; after release the state is still `FAILED` and no artifact is added |

**F. Expiry and limits**

| # | Case | Request / setup | Expected |
|---|---|---|---|
| F1 | Reply inside the TTL | clock +14 min, resume | HTTP 200 |
| F2 | Waiting task timeout | clock +16 min, resume; then `GET` | HTTP 400 as D2 (terminal state `FAILED`); `GET` shows `FAILED`, `task timed out waiting for input`, timestamp t0+15 min |
| F3 | TTL restarts per round | resume at +14 min, handler asks again at t1; reply at t1+14 min | HTTP 200 |
| F4 | Reply after deletion | clock +76 min; resume and `GET` | HTTP 404, `TASK_NOT_FOUND` |
| F5 | Terminal TTL | `COMPLETED`; `GET` at +59 min and at +61 min | 200; 404 |
| F6 | Sweep | more than 1000 tasks including a live waiting task, a `WORKING` task, expired and fresh terminal tasks | the waiting and the working task survive; expired terminal tasks go first, then the oldest; `len <= 500` plus the two |
| F7 | Waiting limit | 100 tasks waiting; one more handler asks | that task is `FAILED`, `too many tasks waiting for input`; 100 still wait |
| F8 | Round limit | handler always asks | the ninth request for input gives `FAILED`, `too many input requests` |
| F9 | Tasks without `fsm` | the tasks of `evict_cov_test.go` | the sweep does not panic; the existing assertions hold |

**G. History length**

| # | Case | Request / setup | Expected |
|---|---|---|---|
| G1 | On send | three-message task; `configuration.historyLength` 0, 1, unset | no `history` field; the last message; all three |
| G2 | On get | `?historyLength=1`, `=0`, `=-1`, `=x` | the last message; no `history`; HTTP 400; HTTP 400 |
| G3 | Stored history | `GET` without the parameter after G1 | all messages |

**H. Build and static checks**

| # | Check |
|---|---|
| H1 | `gofmt -l`, `go vet ./...` (copylocks on `Task`), `go test -race ./internal/a2a/... ./internal/server/...` |
| H2 | `CGO_ENABLED=0 go build ./cmd/gleann`; `go build -tags "treesitter" ./cmd/gleann` |
| H3 | `internal/doccheck`: no new route and no new `GLEANN_*` variable, so `docs/api.md` and `docs/env-vars.md` need no entry |

**I. First user (only with D2)**

| # | Case | Request / setup | Expected |
|---|---|---|---|
| I1 | Asks | two accessible indexes and one not exposed; no index in the metadata | `INPUT_REQUIRED`; the prompt names the two and not the third |
| I2 | Answer by name | follow-up with an index name, other case | answer from that index for the original question; the model is called once |
| I3 | No match | follow-up with other text | `INPUT_REQUIRED` again |
| I4 | No question needed | `metadata.index` set; one accessible index; message with a file part | single-turn result as today |

## 7. Steps (one commit each)

1. Ground work, no change of the wire format: `now` on `Server`; the unexported task fields set at creation and on every transition; `snapshot` under the lock in `handleSendMessage` and `handleGetTask`; the handler call moved into a function with `recover`. Client-visible: a handler panic becomes a `FAILED` task. Tests: existing suite, A8, a `-race` test of `GET` during `message:send`.
2. Error body (D1): `ErrorBody`/`ErrorDetail`, `writeA2AError` with status, reason and metadata. Tests: D8; `server_h2_test.go` line 76.
3. `Message.taskId`/`contextId`, the stored form of incoming messages, client `contextId` on a new task, the lookup with the error table of 2.4, `historyLength`. No task can wait at this step, so the tests place tasks in the store directly. Client-visible: items 1, 2, 4 and 5 of section 5. Tests: A1–A7, A9, D1–D7, G.
4. `InputRequiredError`, resume, the handler contract, replay, the limits of 2.7. Tests: B, C, E1–E7, F7, F8.
5. Expiry and sweep (2.6). Client-visible: item 6 of section 5. Tests: E8, F1–F6, F9.
6. Documentation and API description (section 8), `CHANGELOG.md`, README status row.
7. Only with D2: `ask-rag` asks for the index (2.10). Tests: I.

Gate for every step: `gofmt`, `go vet ./...`, `go test -race` on `internal/a2a` and `internal/server`, `CGO_ENABLED=0 go build ./cmd/gleann`, and the checklist of AGENTS.md section 4.3 before the last one.

## 8. Files touched

| File | Step | Change |
|---|---|---|
| `internal/a2a/server.go` | 1–5 | everything in sections 2 and 3 |
| `internal/a2a/task_fsm.go` | none | the transitions exist (lines 49, 51–54, 83–87) |
| `internal/a2a/multiturn_test.go` (new) | 1–5 | groups A–G |
| `internal/a2a/server_h2_test.go` | 2 | line 76; D8 |
| `internal/a2a/evict_cov_test.go` | 5 | re-checked (F9) |
| `internal/server/openapi_schemas.go` | 6 | `A2AMessage` gains `taskId`, `contextId` (lines 569–576); `A2ASendMessageRequest` gains `configuration`; an error schema |
| `internal/server/openapi_paths.go` | 6 | `message:send` 200 is `{"task": A2ATask}`, not the bare task (lines 973–981), plus 404; `tasks/{id}` gains `historyLength` |
| `docs/a2a.md` | 6 | multi-turn section; the limits and durations of 2.6 and 2.7; tasks are in memory and lost on restart; the access model of 2.8; after an ambiguous failure `GET` the task or re-send with the same `messageId`. Corrections of existing text: the examples send `"role": "user"` and no `messageId` (lines 36–37, 46, 57, 105); `metadata.skill` is shown inside `message` but is read at request level (lines 43–49 against `server.go`, lines 141 and 230), so the documented override has no effect; line 110 reads `task["status"]["message"]` although the response is `{"task": {...}}` with the result in `artifacts`; line 19 says four skills, eight are registered; line 5 links the old spec location |
| `docs/api.md` | 6 | A2A section (lines 163–171): multi-turn note; no new route |
| `README.md` | 6 | status row, line 204 |
| `CHANGELOG.md` | 6 | the changes of section 5 |
| `docs/architecture.md` | 6 | line 295, only if D0 lifts the freeze |
| `internal/server/a2a_handler.go`, `internal/server/a2a_skills_test.go` | 7 | only with D2 |

## 9. Risks

- **A mechanism without a user.** Without D2 no built-in skill reaches `INPUT_REQUIRED`; the wire behaviour is tested with stub handlers only, and the limits of 2.7 cannot be sized against a real case.
- **Request timeout.** `message:send` has the 60 s default while an `ask-rag` turn calls an LLM; the `/ask` endpoints get 300 s (`timeout.go`, lines 39–42). After a 504 the handler keeps running. For a follow-up the replay rule and `GET` cover it. For a first turn the client never learns the task ID; if that task ended in `INPUT_REQUIRED` it occupies one of the 100 waiting places until its TTL. Decision D4.
- **Shared tasks.** Any `query`-scope token that learns a task ID can read the task and answer it (2.8). Decision D3.
- **Strict clients.** The error body changes for two existing errors (D1). Whether any client in use reads `error.status` or parses `Task` or `Message` strictly is not known.
- **Re-invocation.** A handler that does work before asking repeats it on the next turn. The contract (2.3.1) forbids it; nothing enforces it.
- **Lazy expiry.** An expired task is failed or deleted when it is looked up or when the store passes 1000 tasks. On a quiet server its memory stays allocated until then; the count bounds of 2.6 and 2.7 still hold.
- **Unmeasured limits.** The values of 2.7 are chosen, not measured (D6).
- **Scope of the component.** `docs/architecture.md`, line 295, lists `internal/a2a` as frozen (D0).

## 10. Known deviations from A2A v1.0 not addressed here

Found while auditing; each is outside multi-turn and stays as it is.
- **Cancel Task** (spec 3.1.5, `POST /tasks/{id}:cancel`) is not implemented. A waiting task ends only by a reply or by its TTL, and `TASK_STATE_CANCELED` cannot be produced, although the FSM permits the transition (`task_fsm.go`, lines 48 and 53). Decision D5.
- **Streaming stubs.** With `streaming: false`, attempts to use `message:stream` or `tasks/{id}:subscribe` MUST get `UnsupportedOperationError` (spec 3.3.4, line 574). Today `POST /a2a/v1/message:stream` falls to the catch-all and gets a plain-text 404 (`internal/server/server.go`, lines 363–367), and `GET /a2a/v1/tasks/<id>:subscribe` is answered as an unknown task. Read from the code, not run.
- **`configuration.returnImmediately`** is ignored; the server always blocks (spec 3.2.2, line 448).
- **File parts** are decoded as `parts[].file{name,mimeType,bytes}` (`server.go`, lines 115–127); v1.0 has `raw`/`url`/`data` with `filename` and `mediaType` (proto lines 224–242). A v1.0 file-only message is rejected, on a first turn and on a resumed one.
- **Agent card field names:** `security` is emitted where the proto has `security_requirements`, and the security scheme is not the v1.0 wrapper object (`server.go`, lines 39–48; proto lines 383, 503–508).
- **`A2A-Version`** is never read (spec 3.6.2: an empty value means 0.3; an unsupported version MUST get `VersionNotSupportedError`).
- **List Tasks** (spec 3.1.4) is missing.
- **Content type:** responses are `application/json`; the binding says `application/a2a+json` SHOULD be used (spec 11.1).

## 11. Decisions for the maintainer

The text above is written with the recommended answer. Each decision changes only the places named.

| # | Question | Recommendation | Affects |
|---|---|---|---|
| D0 | `docs/architecture.md`, line 295, lists A2A as frozen (maintenance only); `docs/releases/v1.20.1.md` says multi-turn is planned for v1.21. Is the freeze lifted for this RFC? | Say so in `docs/architecture.md` in step 6, or withdraw the RFC | whether to build |
| D1 | Error body: google.rpc name in `error.status` plus `details` for all A2A errors, or keep `error.status` and only add `details`? | The first. The surface is documented as experimental, one existing test assertion changes, and the new error rows would otherwise change twice | 2.4, step 2 |
| D2 | Does `ask-rag` ask for the index in this RFC (2.10)? | Yes, as the last step. Without it nothing reaches `INPUT_REQUIRED` outside tests. The cost is the changed single-turn answer for clients with several indexes and no index in the metadata | 2.10, step 7 |
| D3 | Tasks shared by all `query`-scope tokens (documented), or bound to the creating token? | Shared, documented. Binding needs the token identity passed from `internal/server/security.go` and `internal/auth` and belongs in its own RFC | 2.8 |
| D4 | Should `/a2a/v1/message:send` use the ask timeout (300 s) instead of the 60 s default? | Yes, as a separate one-line commit in `pickTimeout`; it is independent of this RFC and decides how often follow-ups are re-sent | 2.9, section 9 |
| D5 | Cancel Task in this RFC, or with the other conformance items? | With the others. Note for whoever builds it: Go's `ServeMux` requires a wildcard to be a whole path segment, so `POST /a2a/v1/tasks/{id}:cancel` cannot be registered as a pattern; the suffix has to be split from the path value | section 10 |
| D6 | Are the values acceptable: 100 waiting tasks, 8 input requests, 64 KiB follow-up text, 4 KiB prompt, 15 min / 30 min / 1 h? | Yes as constants for the experimental phase | 2.6, 2.7 |
| D7 | Should the deviations of section 10 go into a separate conformance RFC? | Yes, if A2A stays in scope after D0 | section 10 |

## 12. Audit

- 2026-11-20: the draft was audited from three lenses (protocol conformance, feasibility, failure modes). The record with every finding, the evidence checked and its disposition is `gleann-review/progress/wave-2026-11-20/007-audit.md`. Nothing was built or run for the audit; code claims are from reading the working tree (main at v1.20.1 plus uncommitted work), spec claims from the two files named in the header.
- What the audit confirmed in the draft: `taskId` on the `Message` and not on the request envelope; the HTTP statuses 404 and 400; rejecting a message to a `WORKING` task; `contextId` handling on resume; the check-and-claim under one server lock without a per-task mutex; no stream endpoint; no build tag or platform impact; the line references of section 1.
- What changed, by section:
  - Header and section 1: the conformance claim narrowed to what is implemented; item 3 and item 5 corrected (a `contextId` is silently replaced today; waiting tasks would never be dropped, not "unpredictably"); the silent first-index choice added as the concrete failure; the statement that no built-in skill asks.
  - 2.1: client `contextId` on a new task kept; stored form of incoming messages (`messageId`, role, IDs, no file bytes); `metadata.skill` on resume; `historyLength`.
  - 2.2: detection with `errors.As`; nil and empty prompt; prompt size; server-authored messages carry `messageId`, `taskId`, `contextId`; why the prompt is in both places.
  - 2.3 and new 2.3.1: status replaced as a whole; the handler contract (what `Query`, `History`, `Metadata`, `Files` hold on a resumed turn; no side effect before asking; prompt content).
  - 2.4: error body per spec 11.6; the open point closed; row for invalid requests; order of checks; which responses are not A2A envelopes.
  - 2.5: handler run with `recover`; outcome applied only if the task is still `WORKING`; replay by `messageId`; the two code locations that read a task outside the lock and the snapshot that replaces them.
  - 2.6: expiry at lookup with an injected clock and no goroutine; the contradiction between the draft's 2.4 row 1 (expired: 404) and 2.6 (expired: `FAILED`, kept 1 hour) resolved; a `WORKING` age limit; constants instead of "configurable"; tasks without `fsm`.
  - New 2.7 to 2.10: limits and the resulting memory bound; memory-only storage and the access model; what is not changed; the optional first user.
  - Section 3: `SkillID` and `UpdatedAt` are no longer JSON fields; `SendMessageConfiguration`; error types; clock.
  - Section 5: renamed to the heading AGENTS.md asks for; the list of visible changes; the tests that change (the draft said none).
  - Section 6: 12 rows became groups A to I; the concurrency row states how the race is forced; time rows use the injected clock.
  - New sections 7 to 11: steps, files touched, risks, known deviations, decisions.
- Where auditors disagreed, the choice and its reason are in the audit record under "Disagreements".
- Not verified: how strictly published client SDKs parse unknown `Task` fields or a missing `messageId`; the A2A tag v1.1.0-rc was not read; no code was run.
