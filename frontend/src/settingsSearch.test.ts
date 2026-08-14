import { describe, expect, it } from "vitest";
import { filterSettings, settingsSearchEntries } from "./settingsSearch";

describe("settingsSearch governance section", () => {
  it("uses Approvals as the English section name and still matches the old English names", () => {
    const entries = settingsSearchEntries("en", [], []);
    const section = entries.find((entry) => entry.id === "section:governance");
    expect(section?.title).toBe("Approvals");
    expect(section?.title).not.toMatch(/governance and|governance &/i);

    const hits = (query: string) => filterSettings(entries, query).filter((entry) => entry.section === "governance");
    expect(hits("approvals").some((entry) => entry.id === "section:governance")).toBe(true);
    expect(hits("governance").length).toBeGreaterThan(0);
    expect(hits("governance and approval").length).toBeGreaterThan(0);
    expect(hits("governance & approvals").length).toBeGreaterThan(0);
  });

  it("keeps the Chinese section name 治理与审批", () => {
    const entries = settingsSearchEntries("zh-CN", [], []);
    expect(entries.find((entry) => entry.id === "section:governance")?.title).toBe("治理与审批");
    expect(filterSettings(entries, "治理与审批").some((entry) => entry.id === "section:governance")).toBe(true);
  });
});

describe("settingsSearch usage section", () => {
  it("finds the usage page in Chinese and English", () => {
    const zh = settingsSearchEntries("zh-CN", [], []);
    const en = settingsSearchEntries("en", [], []);
    expect(zh.find((entry) => entry.id === "section:usage")?.title).toBe("用量");
    expect(en.find((entry) => entry.id === "section:usage")?.title).toBe("Usage");
    expect(filterSettings(zh, "token 记录").some((entry) => entry.section === "usage")).toBe(true);
    expect(filterSettings(en, "tokens").some((entry) => entry.id === "section:usage")).toBe(true);
    expect(filterSettings(en, "cache").some((entry) => entry.id === "usage:ledger")).toBe(true);
    expect(zh.find((entry) => entry.id === "usage:activity")?.title).toBe("Token 活动");
    expect(filterSettings(zh, "热力").some((entry) => entry.id === "usage:activity")).toBe(true);
  });
});
