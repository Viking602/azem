export const CHAT_UI_FONT_MIN = 12;
export const CHAT_UI_FONT_MAX = 20;
export const CHAT_UI_FONT_DEFAULT = 13;
export const CHAT_CODE_FONT_MIN = 11;
export const CHAT_CODE_FONT_MAX = 18;
export const CHAT_CODE_FONT_DEFAULT = 12;
export const CHAT_UI_FONT_STORAGE_KEY = "azem:chat-font-size";
export const CHAT_CODE_FONT_STORAGE_KEY = "azem:chat-code-font-size";

export function clampChatUIFontSize(value: number): number {
  if (!Number.isFinite(value)) return CHAT_UI_FONT_DEFAULT;
  return Math.min(CHAT_UI_FONT_MAX, Math.max(CHAT_UI_FONT_MIN, Math.round(value)));
}

export function clampChatCodeFontSize(value: number): number {
  if (!Number.isFinite(value)) return CHAT_CODE_FONT_DEFAULT;
  return Math.min(CHAT_CODE_FONT_MAX, Math.max(CHAT_CODE_FONT_MIN, Math.round(value)));
}

export function applyChatTypography(uiSize: number, codeSize: number) {
  if (typeof document === "undefined") return;
  document.documentElement.style.setProperty("--chat-ui-font-size", `${clampChatUIFontSize(uiSize)}px`);
  document.documentElement.style.setProperty("--chat-code-font-size", `${clampChatCodeFontSize(codeSize)}px`);
}

export function chatTypographyVars(uiSize: number, codeSize: number): Record<`--${string}`, string> {
  return {
    "--chat-ui-font-size": `${clampChatUIFontSize(uiSize)}px`,
    "--chat-code-font-size": `${clampChatCodeFontSize(codeSize)}px`,
  };
}
