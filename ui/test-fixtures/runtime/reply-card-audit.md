Run `npm --prefix ui run dev -- --host 127.0.0.1 --port 5206 --strictPort`
and open `/test-fixtures/runtime/reply-card-audit.html`. This imports the actual
production entry point, owner gate and all production CSS. HTTP/SSE data is
synthetic; unexpected mutations and reads throw. No real server is contacted.

Use keyboard Enter on a message's Reply action (the toolbar appears on focus).
The fixture accepts a send into its synthetic server store immediately, then
holds its response until **Acknowledge send** or **Lose response** is pressed.
The store survives page reload and deduplicates by the actual client message ID.
**Select synthetic attachment** drives the actual Composer file-change handler;
uploaded attachment IDs persist with the draft. **Reset fixture** clears only
this loopback origin's session storage.

Acceptance matrix:

1. Reply, type, send, switch to B and type a B draft, acknowledge, switch to A.
   A's content/reply are cleared; B's draft survives. Reload: A remains empty.
2. Reply, send, switch B then A before acknowledgement, edit and cancel/change
   the reply. Acknowledgement preserves the new draft/reply choice.
3. Reply with synthetic attachment, send, lose response, reload. Reply and
   uploaded attachment remain. Retry: `unique=1`; acknowledge: both clear.
4. At 1280px open **已发给 Synthetic B**. Composer, sidebar collapse and chat
   switching work. Tab leaves the card and reaches the workspace. Escape closes
   it. Opening Bot details closes the forwarded card.
5. With the card open and composer focused, resize to 390px. The workspace is
   inert, the dialog has `aria-modal=true`, and focus moves to its close button.
   Tab from the last close button wraps to the first; Shift+Tab wraps back.
   Escape and the X restore focus to the original forwarded-chat button.

`npm --prefix ui run test:reply-ack` independently executes the emitted actual
Composer submit, Workspace send and draft storage functions with delayed synthetic
responses, covering reply/attachment reload identity and newer draft preservation.

For preserved reliability behavior, open `/test-fixtures/runtime/reliability-controls.html`.
Its controls drive the existing full-App synthetic reliability fixture, now with
the production header/card CSS. Check expiry → renewed approval → resume,
reconnect → current pending question, and lost session → synthetic login → current
pending question. These approvals apply exclusively to the in-memory fixture.
