# Why self-host TOFI

This page is honest on purpose. It says where TOFI is a good choice and where it is not.

## What TOFI does better

- **Your server holds the data.** Memory, schedules, files, logs and credentials stay on a Linux server you own. Model requests go only to the provider you set.
- **Bots can reach your network.** A Bot can use Home Assistant, a NAS, internal tools and local model servers. MCP servers on your LAN do not need a public address. Cloud Bot products run in the vendor's cloud and cannot do this.
- **You choose the model.** Sign in with ChatGPT, use an OpenAI or Anthropic key, or connect Ollama, LM Studio, vLLM, OpenRouter or any OpenAI-compatible server. Start with no cloud account if you use a local model.
- **Isolation is the default.** With KVM, each account gets its own Firecracker microVM. Accounts cannot see each other's computers. Browser sign-ins stay inside the account's computer.
- **One server, several people.** Invite-only accounts, an Admin console and per-account data isolation let a family or a small team share one server.
- **Bots work as a group.** In a group chat, the Bot you ask becomes the lead and assigns parts to other Bots. A plan card shows each part and its owner.
- **Safe operations.** `tofi backup` and `tofi restore` export and restore everything. `tofi update` makes a backup first and rolls back if the update fails.
- **Open source.** The server is Apache-2.0. The web app is PolyForm Shield.

## What TOFI does worse

- **Phone.** TOFI has a responsive web app and no native app. The default self-signed certificate needs a one-time browser warning on each device.
- **Setup effort.** You need a Linux box. We estimate 8 to 15 minutes, but we have not yet recorded a timed install. Products like Dots or Muse need no install.
- **Community and brand.** TOFI is new and small. OpenClaw has a much larger community.
- **Model cost and quality.** You pay your own model provider. Cloud Bot products include model cost in a subscription. Small local models (7B to 14B) are weaker at tool use and slower. We have not yet measured a full TOFI run on a local model.
- **Skills and chat apps.** OpenClaw has a large skill library and works inside chat apps such as WhatsApp and Telegram. TOFI has its own web app, a Discord integration, MCP and a small set of built-in skills. TOFI has no public skill marketplace, by design: it limits the risk of malicious skills.
- **Disk.** A Bot computer reserves disk for each account.
- **Known security gap.** The shell in a Bot computer and the shared Chrome debugging port run as the same user. See [SECURITY.md](SECURITY.md).

## When to use something else

- Use [Grok Bot](https://docs.x.ai/grok-bot/overview), [OpenAI Dots](https://learn.chatgpt.com/docs/dots) or Meta Muse when you want a managed product with phone apps and no server to run.
- Use [OpenClaw](https://github.com/openclaw/openclaw) when you want the largest community and skill library, or an agent that lives in your chat apps. Read its [sandboxing guide](https://docs.openclaw.ai/gateway/sandboxing) first. Tools run on the host unless you set up a sandbox.
- Use a hosted product when you cannot run and update a server.

## What to hand over to a Bot

We ran a fixed set of tasks on a live install. We ran each task 3 or 4 times with the default conversation model (Claude Sonnet 5.5). The sample is small. "All passed" means we have not seen it fail. It does not mean a guarantee. For 4 passes out of 4, the 95% interval is 51% to 100%. A grader model scored the results, and it is strict.

### Hand over

Short jobs that one reply can finish and that change nothing outside the Bot's computer.

- Write a small script on the Bot's computer, run it and show the output.
- Make a printable checklist.
- Convert units and give a small table.
- Remind you of something in a few minutes.
- Answer whether an open-source project is still maintained.
- Look up how to read an unfamiliar name.

### Glance

The Bot usually did these. Check the result before you use it.

- Draft a letter. Review the draft before you send it.
- Two Bots write and check a text for you.
- Give a technology recommendation. Once, a date had no source.
- Send a daily news digest, or watch for a new release. Once, the Bot made a duplicate schedule.
- Sort a folder of contracts into an index with due dates.
- Make a report with charts from sales data.
- Build a one-file web page. Once, the layout was cramped at phone width.

### Not yet

Do not expect these to work without help.

- Reminders with a time zone. The times were right, but the reply did not state the time zone.
- Facts about something the Bot may not know. Once, the Bot answered from memory and did not search.
- Turn a quarter of invoices (PDF and photos) into a spreadsheet and flag the problems. All 4 runs failed, in 4 different ways.
- Watch competitor pricing pages weekly. The work was right, but the reply did not say where the baseline is stored.
- A three-Bot group that writes, designs and checks an announcement. The result was usable, but the Bots did not split the work as planned.
- A 10-page client slide deck. The check may be too strict, so this is not proven.

### When a Bot asks first

TOFI drafts email. It never sends email for you. You send it. For other actions, the rules differ:

- **Hard rules.** Sign-ins, two-step codes and CAPTCHAs: the Bot asks you to do them. Installing a connection or skill needs your approval. A Bot that works in the background without you cannot approve its own request.
- **Judged by a model.** Clicks on buy, send, publish or delete buttons in a web page, and write calls to outside services (MCP), go to a review model first. If you clearly asked for the action, it can go ahead. If not, the Bot asks you. This relies on the model labeling the action correctly. It is not a hard guarantee.
- **No gate yet.** Commands that a Bot runs in its own computer shell (for example `curl` or `ssh`) have no approval step. A Bot can only use the keys and connections you give it. Give it only what you accept it using.

We update this page when we run the task set again.
