---
name: web-tasks
description: Forms, orders, bookings and account or settings changes on websites, when no connected tool fits.
requires: [computer]
kind: procedure
---

# Doing things on websites

## Route

- When a connected tool covers the task, use it and skip the page. This Skill is for work that no connected tool covers, or for a connected tool that fails or is not authorized.

## On the page

- Fields are filled with browser.click and desktop.type. A field's existing text is selected first (key ctrl+a) so that typing replaces it.
- Every click, type and key carries its real effect in the effect parameter. The rules for effects and approvals are in that parameter's text and apply to every page action.
- After submitting, the confirmation page is read and the reply says what happened.
