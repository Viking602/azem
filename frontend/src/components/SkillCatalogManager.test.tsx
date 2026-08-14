import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { SkillEntry } from "../types";
import SkillCatalogManager from "./SkillCatalogManager";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const skills: SkillEntry[] = [
  { name: "verify", description: "验证当前工作区", sourcePath: "bundled/verify", bundled: true, eager: true, disabled: false, modelVisible: true, resourceCount: 2, logoPath: "data:image/svg+xml;base64,PHN2Zz4=" },
  { name: "unused-design", description: "当前项目不需要加载", sourcePath: "~/.codex/skills/unused-design", bundled: false, eager: false, disabled: true, modelVisible: false, resourceCount: 4 },
];

describe("SkillCatalogManager", () => {
  afterEach(() => vi.clearAllMocks());

  it("keeps disabled skills manageable and enables them from the filtered list", async () => {
    const onReload = vi.fn(() => Promise.resolve());
    const onSetEnabled = vi.fn(() => Promise.resolve());
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<SkillCatalogManager skills={skills} language="zh-CN" onReload={onReload} onSetEnabled={onSetEnabled} />));
    expect(container.querySelector(".skill-manager-count")?.textContent).toContain("1/ 2");
    expect(container.querySelectorAll(".skill-manager-list article")).toHaveLength(2);
    expect(container.querySelector('.skill-manager-icon img[src^="data:image/svg+xml;base64,"]')).not.toBeNull();

    const disabledFilter = Array.from(container.querySelectorAll<HTMLButtonElement>(".skill-filters button")).find((button) => button.textContent?.includes("已停用"))!;
    await act(async () => disabledFilter.click());
    expect(container.querySelectorAll(".skill-manager-list article")).toHaveLength(1);
    expect(container.querySelector(".skill-manager-list article")?.textContent).toContain("unused-design");

    await act(async () => container.querySelector<HTMLButtonElement>('[role="switch"][aria-label="启用 unused-design"]')!.click());
    expect(onSetEnabled).toHaveBeenCalledWith("unused-design", true);

    await act(async () => container.querySelector<HTMLButtonElement>('.skill-reload')!.click());
    expect(onReload).toHaveBeenCalledTimes(1);

    await act(async () => root.unmount());
    container.remove();
  });
});
