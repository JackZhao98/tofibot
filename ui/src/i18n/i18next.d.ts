import "i18next";
import type common from "../locales/en/common.json";
import type auth from "../locales/en/auth.json";
import type chat from "../locales/en/chat.json";
import type tasks from "../locales/en/tasks.json";
import type schedules from "../locales/en/schedules.json";
import type work from "../locales/en/work.json";
import type settings from "../locales/en/settings.json";
import type extensions from "../locales/en/extensions.json";
import type computer from "../locales/en/computer.json";
import type bots from "../locales/en/bots.json";
import type mail from "../locales/en/mail.json";

// English catalogs are the key schema: a key missing from en is a type error.
declare module "i18next" {
  interface CustomTypeOptions {
    defaultNS: "common";
    resources: { common: typeof common; auth: typeof auth; chat: typeof chat; tasks: typeof tasks; schedules: typeof schedules; work: typeof work; settings: typeof settings; extensions: typeof extensions; computer: typeof computer; bots: typeof bots; mail: typeof mail };
  }
}
