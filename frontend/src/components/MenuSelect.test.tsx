import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
// @ts-expect-error Vitest runs in Node; production TypeScript intentionally excludes Node types.
import { readFileSync } from "node:fs";
import MenuSelect from "./MenuSelect";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const prototypeStyles = readFileSync("src/prototype.css", "utf8");

async function openMenu(details: HTMLDetailsElement) {
  details.open = true;
  await act(async () => {
    details.dispatchEvent(new Event("toggle", { bubbles: true }));
  });
}

describe("MenuSelect", () => {
  it("portals the menu, selects an option, and closes outside", async () => {
    const change = vi.fn();
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(
      <MenuSelect
        value="single"
        options={[{ value: "single", label: "默认模式" }, { value: "team", label: "多代理" }]}
        onChange={change}
        ariaLabel="Agent mode"
      />,
    ));

    const details = container.querySelector("details")!;
    expect(details.querySelector("summary")?.getAttribute("title")).toBeNull();
    await openMenu(details);
    const options = document.body.querySelectorAll<HTMLButtonElement>(".menu-select-options-portal .menu-select-option");
    expect(options.length).toBe(2);
    expect(Array.from(options).every((option) => option.getAttribute("title") === null)).toBe(true);
    expect(options[0]!.querySelector(".menu-select-check")).toBe(options[0]!.lastElementChild);
    expect(options[0]!.classList.contains("selected")).toBe(true);
    await act(async () => options[1]!.click());
    expect(change).toHaveBeenCalledWith("team");
    expect(details.open).toBe(false);
    expect(document.body.querySelector(".menu-select-options-portal")).toBeNull();
    expect(container.querySelector("select")).toBeNull();

    await openMenu(details);
    expect(document.body.querySelector(".menu-select-options-portal")).not.toBeNull();
    await act(async () => document.body.dispatchEvent(new MouseEvent("pointerdown", { bubbles: true })));
    expect(details.open).toBe(false);
    expect(document.body.querySelector(".menu-select-options-portal")).toBeNull();
    await act(async () => root.unmount());
    container.remove();
  });

  it("portals into an open dialog so modal settings menus stay clickable", async () => {
    const change = vi.fn();
    const dialog = document.createElement("dialog");
    // jsdom lacks showModal; portalRoot only needs dialog.open === true.
    Object.defineProperty(dialog, "open", { configurable: true, get: () => true });
    document.body.append(dialog);
    const root = createRoot(dialog);
    await act(async () => root.render(
      <MenuSelect
        value="a"
        options={[{ value: "a", label: "A" }, { value: "b", label: "B" }]}
        onChange={change}
        ariaLabel="Model"
      />,
    ));

    const details = dialog.querySelector("details")!;
    await openMenu(details);
    expect(document.body.querySelector(":scope > .menu-select-options-portal")).toBeNull();
    const options = dialog.querySelectorAll<HTMLButtonElement>(".menu-select-options-portal .menu-select-option");
    expect(options.length).toBe(2);
    await act(async () => options[1]!.click());
    expect(change).toHaveBeenCalledWith("b");
    await act(async () => root.unmount());
    dialog.remove();
  });

  it("positions a dialog menu relative to its portal instead of adding viewport offsets twice", async () => {
    const dialog = document.createElement("dialog");
    Object.defineProperty(dialog, "open", { configurable: true, get: () => true });
    dialog.getBoundingClientRect = () => ({
      x: 100, y: 40, left: 100, top: 40, right: 900, bottom: 640,
      width: 800, height: 600, toJSON: () => ({}),
    });
    document.body.append(dialog);
    const root = createRoot(dialog);
    await act(async () => root.render(
      <MenuSelect
        value="system"
        options={[{ value: "system", label: "系统默认" }, { value: "font", label: "字体" }]}
        onChange={() => {}}
        ariaLabel="界面字体"
        fit="full"
        searchable
      />,
    ));

    const details = dialog.querySelector<HTMLDetailsElement>("details")!;
    const summary = details.querySelector<HTMLElement>("summary")!;
    summary.getBoundingClientRect = () => ({
      x: 560, y: 400, left: 560, top: 400, right: 760, bottom: 440,
      width: 200, height: 40, toJSON: () => ({}),
    });
    await openMenu(details);

    const panel = dialog.querySelector<HTMLElement>(".menu-select-options-portal")!;
    expect(panel.style.position).toBe("absolute");
    expect(panel.style.left).toBe("460px");
    expect(panel.style.top).toBe("406px");
    await act(async () => root.unmount());
    dialog.remove();
  });

  it("keeps route controls contained and governance typography token-driven", () => {
    expect(prototypeStyles).toMatch(/\.route-reasoning-menu > summary\s*\{[^}]*min-width:\s*0;/s);
    const governanceStart = prototypeStyles.indexOf(".governance-pane");
    const governanceEnd = prototypeStyles.indexOf(".workspace-overview-page", governanceStart);
    const governanceStyles = prototypeStyles.slice(governanceStart, governanceEnd);
    expect(governanceStart).toBeGreaterThanOrEqual(0);
    expect(governanceEnd).toBeGreaterThan(governanceStart);
    expect(governanceStyles).toContain("font-size: var(--text-");
    expect(governanceStyles).not.toMatch(/font-size:\s*\d+px/);
  });

  it("toggles once when WKWebView delivers a release-first trackpad sequence", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(
      <MenuSelect
        value="model-a"
        options={[{ value: "model-a", label: "Model A" }, { value: "model-b", label: "Model B" }]}
        onChange={() => {}}
        ariaLabel="Model route"
      />,
    ));

    const details = container.querySelector<HTMLDetailsElement>("details")!;
    const summary = details.querySelector<HTMLElement>("summary")!;
    const releaseFirst = async (detail: number, timeStamp: number) => {
      const eventAt = <T extends Event>(event: T, value: number) => {
        Object.defineProperty(event, "timeStamp", { value });
        return event;
      };
      await act(async () => {
        summary.dispatchEvent(eventAt(new PointerEvent("pointerup", { bubbles: true, cancelable: true, button: 0, buttons: 0, detail, pointerType: "mouse", isPrimary: true }), timeStamp + 4));
        summary.dispatchEvent(eventAt(new MouseEvent("mouseup", { bubbles: true, cancelable: true, button: 0, buttons: 0, detail }), timeStamp + 4));
        summary.dispatchEvent(eventAt(new PointerEvent("pointerdown", { bubbles: true, cancelable: true, button: 0, buttons: 0, detail, pointerType: "mouse", isPrimary: true }), timeStamp));
        summary.dispatchEvent(eventAt(new MouseEvent("mousedown", { bubbles: true, cancelable: true, button: 0, buttons: 0, detail }), timeStamp));
      });
      await act(async () => new Promise((resolve) => window.setTimeout(resolve, 0)));
    };

    await act(async () => summary.click());
    expect(details.open).toBe(true);
    await act(async () => summary.click());
    expect(details.open).toBe(false);

    await releaseFirst(2, 500);
    expect(details.open).toBe(true);
    expect(document.body.querySelector(".menu-select-options-portal")).not.toBeNull();
    await act(async () => window.dispatchEvent(new Event("blur")));
    expect(details.open).toBe(false);

    await releaseFirst(3, 700);
    expect(details.open).toBe(true);
    await releaseFirst(4, 900);
    expect(details.open).toBe(false);
    expect(document.body.querySelector(".menu-select-options-portal")).toBeNull();

    await act(async () => root.unmount());
    container.remove();
  });
});
