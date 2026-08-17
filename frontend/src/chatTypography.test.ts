import { describe, expect, it } from "vitest";
import {
  CHAT_CODE_FONT_DEFAULT,
  CHAT_CODE_FONT_MAX,
  CHAT_CODE_FONT_MIN,
  CHAT_UI_FONT_DEFAULT,
  CHAT_UI_FONT_MAX,
  CHAT_UI_FONT_MIN,
  applyChatTypography,
  clampChatCodeFontSize,
  clampChatUIFontSize,
} from "./chatTypography";

describe("chat typography settings", () => {
  it("defaults and clamps conversation UI and code sizes independently", () => {
    expect(CHAT_UI_FONT_DEFAULT).toBe(13);
    expect(CHAT_CODE_FONT_DEFAULT).toBe(12);
    expect(clampChatUIFontSize(Number.NaN)).toBe(CHAT_UI_FONT_DEFAULT);
    expect(clampChatUIFontSize(11)).toBe(CHAT_UI_FONT_MIN);
    expect(clampChatUIFontSize(21)).toBe(CHAT_UI_FONT_MAX);
    expect(clampChatCodeFontSize(Number.NaN)).toBe(CHAT_CODE_FONT_DEFAULT);
    expect(clampChatCodeFontSize(10)).toBe(CHAT_CODE_FONT_MIN);
    expect(clampChatCodeFontSize(19)).toBe(CHAT_CODE_FONT_MAX);
  });

  it("writes the chat-surface CSS variables", () => {
    applyChatTypography(16, 14);
    expect(document.documentElement.style.getPropertyValue("--chat-ui-font-size")).toBe("16px");
    expect(document.documentElement.style.getPropertyValue("--chat-code-font-size")).toBe("14px");
    document.documentElement.style.removeProperty("--chat-ui-font-size");
    document.documentElement.style.removeProperty("--chat-code-font-size");
  });
});
