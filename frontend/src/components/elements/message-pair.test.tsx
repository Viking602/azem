import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it } from "vitest";
import {
  MessagePairAssistant,
  MessagePairError,
  MessagePairProgress,
  MessagePairRoot,
  MessagePairUser,
} from "./message-pair";

it("composes Azem durable blocks through the registry-installed message pair", async () => {
  const container = document.createElement("div");
  const root = createRoot(container);
  await act(async () => root.render(<MessagePairRoot className="session-turn">
    <MessagePairUser data-session-sequence={1}><p>Question</p></MessagePairUser>
    <MessagePairProgress className="active" data-state="active">Working</MessagePairProgress>
    <MessagePairAssistant className="streaming" aria-busy="true" data-session-sequence={2}>Answer</MessagePairAssistant>
    <MessagePairError role="alert">Failed</MessagePairError>
  </MessagePairRoot>));

  expect(container.querySelector('[data-slot="message-pair"]')?.classList.contains("session-turn")).toBe(true);
  const user = container.querySelector('[data-slot="user-message"]');
  expect(user?.classList.contains("user-block")).toBe(true);
  expect(user?.getAttribute("data-session-sequence")).toBe("1");
  const progress = container.querySelector('[data-slot="assistant-progress"]');
  expect(progress?.classList.contains("commentary-block")).toBe(true);
  expect(progress?.getAttribute("data-state")).toBe("active");
  const assistant = container.querySelector('[data-slot="assistant-message"]');
  expect(assistant?.classList.contains("assistant-block")).toBe(true);
  expect(assistant?.getAttribute("aria-busy")).toBe("true");
  expect(assistant?.getAttribute("data-session-sequence")).toBe("2");
  const error = container.querySelector('[data-slot="error-message"]');
  expect(error?.classList.contains("error-block")).toBe(true);
  expect(error?.getAttribute("role")).toBe("alert");

  await act(async () => root.unmount());
});
