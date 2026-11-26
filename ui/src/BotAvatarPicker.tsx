import * as RadioGroup from "@radix-ui/react-radio-group";
import { useState } from "react";
import { catalog, paletteOptions, type AvatarConfig } from "./lib/tofi-avatar/index.js";
import { BotAvatar, type AvatarMotion } from "./BotAvatar";
import { randomBotAvatarConfig } from "./avatarStore";
import { useTranslation } from "./i18n";
import { Button } from "./ds/Button/Button";

export function BotAvatarPicker({ botId, config, onChange }: { botId: string; config: AvatarConfig; onChange: (config: AvatarConfig) => void }) {
  const { t } = useTranslation("bots");
  const [motion, setMotion] = useState<AvatarMotion>("awake");
  const changeShape = (next: AvatarConfig) => { if (config.shape !== next.shape) onChange(next); };
  const changePalette = (next: AvatarConfig) => { if (config.palette !== next.palette) onChange(next); };
  const palettes = paletteOptions(config.pattern);
  const pattern = catalog.patterns.find((item) => item.id === config.pattern);
  return <section className="bot-avatar-picker bot-avatar-v2" aria-label={t("avatar.aria")}>
    <div className="bot-avatar-stage"><BotAvatar id={botId} config={config} motion={motion} /></div>
    <RadioGroup.Root className="bot-avatar-state" value={motion} onValueChange={value => setMotion(value as AvatarMotion)} aria-label={t("avatar.state_aria")}>{(["sleeping", "awake", "working"] as const).map(value => <RadioGroup.Item value={value} key={value} type="button" >{t(`avatar.state.${value}`)}</RadioGroup.Item>)}</RadioGroup.Root>
    <div className="bot-avatar-sizes" aria-label={t("avatar.sizes_aria")}>{([24, 36, 48, 64] as const).map(size => <div key={size}><span style={{width:size,height:size}}><BotAvatar id={botId} config={config} motion="sleeping" /></span><small>{size}</small></div>)}</div>
    <h3>{t("avatar.palette")} <small>{t("avatar.palette_count", { count: palettes.length })}</small></h3><RadioGroup.Root className="bot-avatar-palettes" value={config.palette} onValueChange={value => void changePalette({...config,palette:value as AvatarConfig["palette"]})} aria-label={t("avatar.palettes_aria")}>{palettes.map(option => <RadioGroup.Item value={option.id} data-palette={option.id} type="button" key={option.id} aria-label={option.name}  title={option.name}><span style={{background:option.body,borderColor:option.mark}}/><small>{option.name}</small></RadioGroup.Item>)}</RadioGroup.Root>
    <div className="bot-avatar-choice-heading"><h3>{t("avatar.shape")}</h3><Button type="button" variant="ghost" size="sm" onClick={() => onChange(randomBotAvatarConfig(botId))}>{t("avatar.random")}</Button></div>
    <RadioGroup.Root className="bot-avatar-shapes" value={config.shape} onValueChange={value => void changeShape({...config,shape:value as AvatarConfig["shape"]})} aria-label={t("avatar.shapes_aria")}>{catalog.shapes.map(shape => <RadioGroup.Item value={shape.id} type="button" key={shape.id} ><BotAvatar id={botId} config={{...config,shape:shape.id}} motion="sleeping"/><span>{shape.name}</span></RadioGroup.Item>)}</RadioGroup.Root>
    <h3>{t("avatar.pattern")}</h3><RadioGroup.Root className="bot-avatar-patterns" value={config.pattern} onValueChange={value => onChange({...config,pattern:value as AvatarConfig["pattern"],palette:paletteOptions(value as AvatarConfig["pattern"])[0].id})} aria-label={t("avatar.patterns_aria")}>{catalog.patterns.map(item => <RadioGroup.Item value={item.id} type="button" key={item.id} >{item.name}</RadioGroup.Item>)}</RadioGroup.Root>
    {pattern?.note && <p className="field-note">{palettes.length === 1 ? `${pattern.note} · ${t("avatar.fixed_palette")}` : pattern.note}</p>}
  </section>;
}
