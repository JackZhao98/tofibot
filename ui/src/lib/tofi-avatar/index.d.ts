export type Shape = 'curl' | 'loaf' | 'tall' | 'rice' | 'bean' | 'puddle' | 'stand';
export type Pattern = 'solid' | 'stripes' | 'beans' | 'halfmask' | 'mask' | 'tortie' | 'cloud' | 'split' | 'saddle' | 'calico' | 'cowblaze' | 'cowpatch' | 'tabby' | 'tipped';
export type Palette = 'ivory' | 'iris' | 'caramel' | 'ocean' | 'moss' | 'berry' | 'slate' | 'honey' | 'lagoon' | 'inkred' | 'inkviolet' | 'calico' | 'cow' | 'tabby' | 'coffee' | 'pointblue' | 'pointlilac' | 'pointcinnamon' | 'pointcharcoal' | 'pointrose';
export type Action = 'look' | 'curious' | 'double-take' | 'slow-blink' | 'happy' | 'stand' | 'blink-look' | 'work' | 'work-end' | 'knead' | 'bat' | 'wave' | 'sleep' | 'dream' | 'breathe' | 'wake';
export type EyeStyle = 'dot' | 'shine' | 'ring' | 'mismatch' | 'tiny' | 'big';
export type Idle = 'asleep' | 'awake';
export interface AvatarConfig {
  schemaVersion: 1;
  shape: Shape;
  pattern: Pattern;
  palette: Palette;
}
export interface PaletteOption {id: Palette;name: string;body: string;mark: string;accent: string;white?: boolean;}
export interface AvatarState {
  phase: 'sleepIdle' | 'awakeIdle' | 'playing';
  idle: Idle;
  action: Action | null;
  reducedMotion: boolean;
  speed: number;
  config: AvatarConfig;
}
export type PlayResult = {action: Action;cancelled: false;state: Idle;skipped: boolean} | {action: Action;cancelled: true;reason: string};
export interface MountOptions extends Partial<AvatarConfig> {
  initialState?: Idle;
  speed?: number;
  reducedMotion?: boolean | 'system';
  life?: boolean;
  autoplay?: boolean;
  eyes?: EyeStyle;
  onState?: (state: AvatarState) => void;
}
export interface CatAvatar {
  play(action: Action): Promise<PlayResult>;
  /** Enable or pause the idle breath, blink, gaze and ear motion. */
  setLife(on: boolean): void;
  /** Enable weighted idle actions for the current cat personality. */
  setAutoplay(on: boolean): void;
  setEyes(style: EyeStyle): EyeStyle;
  eyeStyles(): EyeStyle[];
  clips(): Action[];
  /** Stop animation and render a static debug pose. */
  pose(values: Record<string, number>): void;
  getConfig(): AvatarConfig;
  getState(): AvatarState;
  setAppearance(patch: Partial<AvatarConfig>): AvatarConfig;
  setIdle(state: Idle): void;
  reset(): void;
  setSpeed(speed: number): number;
  /** A script-free snapshot of the CURRENT frame; not an animated SVG. */
  exportSVG(): string;
  destroy(): void;
}
export const version: string;
export const catalog: {
  readonly shapes: ReadonlyArray<{readonly id: Shape;readonly name: string}>;
  readonly patterns: ReadonlyArray<{readonly id: Pattern;readonly name: string;readonly mode: 'general' | 'special';readonly note: string}>;
  readonly actions: ReadonlyArray<Action>;
};
export function paletteOptions(pattern: Pattern): PaletteOption[];
export function normalizeConfig(value?: Partial<AvatarConfig>): AvatarConfig;
export function mountCat(host: HTMLElement, options?: MountOptions): CatAvatar;
