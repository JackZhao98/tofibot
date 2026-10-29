// Drive the app's Select (button.ui-select-trigger + portaled .ui-select-content) from Playwright.
// `trigger` is a Locator for the trigger button, or for a container holding exactly one.
async function resolveTrigger(trigger) {
  const own = await trigger.evaluate(el => el.matches("button.ui-select-trigger")).catch(() => false);
  return own ? trigger : trigger.locator("button.ui-select-trigger").first();
}
export async function selectValue(trigger) {
  return (await resolveTrigger(trigger)).getAttribute("data-value");
}
export async function openSelect(trigger) {
  const button = await resolveTrigger(trigger);
  await button.click();
  const content = button.page().locator(".ui-select-content");
  await content.waitFor();
  return content;
}
export async function closeSelect(trigger) {
  const page = (await resolveTrigger(trigger)).page();
  await page.keyboard.press("Escape");
  await page.locator(".ui-select-content").waitFor({state: "detached"});
}
export async function pickSelect(trigger, value) {
  const button = await resolveTrigger(trigger);
  const content = await openSelect(button);
  await content.locator(`.ui-select-item[data-value="${value}"]`).click();
  await button.page().locator(".ui-select-content").waitFor({state: "detached"});
}
// Items as [{value, text, disabled, group}] in DOM order (opens the list, reads it, closes it).
export async function selectItems(trigger) {
  const button = await resolveTrigger(trigger);
  const content = await openSelect(button);
  const items = await content.evaluate(root => [...root.querySelectorAll(".ui-select-item")].map(el => ({
    value: el.getAttribute("data-value"),
    text: el.querySelector(".ui-select-item-body > span")?.textContent ?? el.textContent.trim(),
    disabled: el.hasAttribute("data-disabled"),
    group: el.closest('[role="group"]')?.querySelector(".ui-select-group-label")?.textContent ?? null,
  })));
  await closeSelect(button);
  return items;
}
export const selectLabels = async trigger => (await selectItems(trigger)).map(item => item.text);
