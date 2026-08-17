import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import MenuSelect from "./MenuSelect";

(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

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
});
