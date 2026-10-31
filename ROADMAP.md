# Roadmap

This is what we plan to work on next. It is a plan, not a promise. Dates are not
fixed. Finished work is in [release-notes/](release-notes/).

## Near term

- **Mac desktop app with personal computers.** A Mac app that pairs with your
  server. Bots can then use your own Mac as another computer, with per-Bot permissions
  decided on the Mac. The server side is in the code and is off by default
  (`TOFI_PERSONAL_COMPUTERS=1`). The Mac app is a separate project and is not
  released yet.
- **E2B Bot computers (planned).** A second kind of Bot computer: a hosted E2B
  sandbox that you rent with your own key. The built-in VM stays the default.
- **Local and OpenAI-compatible models.** Use a model that runs on your own
  hardware, or any service with an OpenAI-compatible API, as the model provider.
- **Workflow templates.** Ready-made Bot and workflow setups that you can start
  from and change.
- **Cloud.** Planned. We have no date and no design to share yet.

## Already available

- Self-host install with one command, or with Docker Compose when the server has
  no KVM ([README](README.md#install)).
- A Bot computer for each account as an optional upgrade. The kind available today
  is the built-in VM (a Firecracker microVM on your server, needs KVM).

## How to help

Open an issue to ask for something or to say which item matters most to you. See
[CONTRIBUTING.md](CONTRIBUTING.md).
